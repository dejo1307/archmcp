// Package crossrepo links the per-repo architectural graphs of an appended
// multi-repo fact set into a single cross-repo "graph of graphs".
//
// In multi-repo (append) mode, facts from several repositories live in one
// store, each tagged with a Repo label, but the graph only ever connects facts
// within a single repo. This package derives the edges *between* repos from
// signals the extractors already emit:
//
//   - HTTP route role matching: a route a repo calls (role="client") whose
//     (path, method) matches a route another repo serves (role="server" or
//     unset) means the caller depends on the servee.
//   - Import / shared-lib references: a dependency whose import target names
//     another loaded repo (by @scope or leading path segment) means the
//     importer depends on that repo.
//   - Shared symbol surface: when two repos declare enough of the same
//     distinctive types (a vendored/shared protocol header, e.g. the onelab
//     GmshClient/GmshServer classes copied between repos), they are coupled.
//     This signal is symmetric, so it is emitted as a bidirectional pair of
//     edges marked via="shared_symbols".
//
// The result is expressed as synthetic facts: one KindService node per repo and
// one KindDependency edge per (consumer -> provider) pair. Because these are
// ordinary facts, they flow into Store.BuildGraph and make every traversal tool
// (traverse, find_path, impact_analysis, query_facts) cross-repo aware with no
// per-tool changes.
package crossrepo

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// SyntheticMarker tags every fact this package emits, so the engine can drop and
// recompute them idempotently on each append.
const SyntheticMarker = "crossrepo"

// maxSamples bounds how many endpoint / import samples an edge fact carries.
const maxSamples = 25

// minSharedSegments is the fewest trailing path segments a client and server
// route must share for a suffix match. Two segments (e.g. "settings/feedback")
// keeps the join specific enough to avoid false positives while tolerating the
// base-path/prefix differences between a server's full path ("/api/settings/...")
// and a client's base-relative call ("settings/...").
const minSharedSegments = 2

// edge accumulates everything justifying one consumer -> provider dependency.
type edge struct {
	consumer   string
	provider   string
	via        map[string]bool // "http", "http-client", "import", "shared_symbols"
	endpoints  map[string]bool // "METHOD /path"
	imports    map[string]bool // sample import targets
	symbols    map[string]bool // sample shared type identities
	confidence string          // "verified" or "probable" — max over HTTP endpoints
}

// httpCoverage tallies, per consumer repo, how many HTTP-client call sites were
// detected and how many resolved to a loaded service. The difference is the
// blind spot: call sites enola saw but could not link to a target, so a service
// with no outbound edges but unresolved>0 is a coverage gap, not truly isolated.
type httpCoverage struct {
	detected int
	resolved int
}

func (e *edge) note(via string) {
	if e.via == nil {
		e.via = map[string]bool{}
	}
	e.via[via] = true
}

// noteConfidence records an HTTP-match confidence, keeping the strongest seen:
// "verified" wins over "probable".
func (e *edge) noteConfidence(c string) {
	if e.confidence == "verified" {
		return
	}
	if c == "verified" || e.confidence == "" {
		e.confidence = c
	}
}

// ComputeLinks analyzes a multi-repo fact set and returns synthetic facts that
// connect repositories. It is pure and deterministic: the same input always
// yields the same output in a stable, sorted order, so callers may recompute it
// idempotently after removing the prior synthetic facts.
func ComputeLinks(all []facts.Fact) []facts.Fact {
	normToLabel := repoLabelLookup(all)
	if len(normToLabel) < 2 {
		return nil // need at least two repos to have a cross-repo edge
	}

	edges := map[string]*edge{}
	cov := map[string]*httpCoverage{}
	linkHTTP(all, edges, cov)
	linkImports(all, normToLabel, edges)
	linkSharedSymbols(all, edges)

	return materialize(edges, repoLabels(normToLabel), cov)
}

// repoLabels returns the actual repo labels (the values of the
// normalized-label lookup), sorted for deterministic output.
func repoLabels(normToLabel map[string]string) []string {
	out := make([]string, 0, len(normToLabel))
	for _, label := range normToLabel {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

// repoLabelLookup returns a map from normalized repo label to the actual label,
// covering every distinct non-empty Repo tag in the fact set.
func repoLabelLookup(all []facts.Fact) map[string]string {
	out := map[string]string{}
	for _, f := range all {
		if f.Repo == "" {
			continue
		}
		out[normalizeLabel(f.Repo)] = f.Repo
	}
	return out
}

// --- signal (A): HTTP route role matching ---

type routeRef struct {
	repo     string
	method   string
	path     string
	fullPath string // the complete normalized server path this ref was indexed from
}

func linkHTTP(all []facts.Fact, edges map[string]*edge, cov map[string]*httpCoverage) {
	// Index server routes by normalized path + method.
	server := map[string][]routeRef{}
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" {
			continue
		}
		if roleOf(f) == "client" {
			continue
		}
		method := normalizeMethod(propString(f, "method"))
		if method == "" {
			continue
		}
		// Index every trailing-segment suffix of each server path, so a client
		// that calls a base-relative subpath ("settings/x") still matches a
		// server serving the full path ("/api/settings/x"). serverPaths already
		// returns normalized paths; fullPath records which full path a suffix
		// came from, so a match can tell a full-path hit from a fragment hit.
		for _, p := range serverPaths(f) {
			ref := routeRef{repo: f.Repo, method: method, path: f.Name, fullPath: p}
			for _, suf := range pathSuffixes(p) {
				key := routeKey(suf, method)
				server[key] = append(server[key], ref)
			}
		}
	}

	// Match client routes against the server index.
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" || roleOf(f) != "client" {
			continue
		}
		// Every client call site is a detected outbound edge. Counting here, before
		// the low-signal filters below, means call sites we choose not to resolve
		// (no method, generic path) and call sites with no matching server both fall
		// into unresolved (detected - resolved) — the blind spot the report exposes.
		covFor(cov, f.Repo).detected++
		method := normalizeMethod(propString(f, "method"))
		if method == "" {
			continue
		}
		np := normalizePath(f.Name)
		if isGenericPath(np) {
			continue
		}
		// Canonicalize the leading slash so a base-relative client path
		// ("settings/x") matches the indexed suffix form ("/settings/x").
		clientPath := canonicalLeadingSlash(np)
		// Try the client path's trailing-segment suffixes against the server suffix
		// index, longest first. The server index already holds suffixes of every
		// server path, so matching client suffixes too makes the join symmetric: it
		// resolves a client call that carries an extra gateway/BFF prefix
		// ("/api/settings/tickets/{}/resolve") to a server serving the un-prefixed
		// path ("/tickets/{}/resolve"), as well as the reverse (a base-relative
		// client calling a longer server path) the index already handled.
		matches, matchedPath := lookupClientMatches(server, clientPath, method)
		provider, unambiguous := pickProvider(f, matches)
		// A non-empty provider means the call site matched a loaded service (a
		// self-match is internal, not a blind spot) — count it resolved either way.
		if provider != "" {
			covFor(cov, f.Repo).resolved++
		}
		if provider == "" || provider == f.Repo {
			continue
		}
		e := edgeFor(edges, f.Repo, provider)
		e.note(httpVia(f))
		if e.endpoints == nil {
			e.endpoints = map[string]bool{}
		}
		e.endpoints[method+" "+f.Name] = true
		e.noteConfidence(matchConfidence(matchedPath, np, provider, matches, unambiguous))
	}
}

// pickProvider resolves which provider repo a client route points at, and
// whether that resolution was unambiguous. With a single candidate repo it
// returns (repo, true); with several it uses the client's service hint
// (target_hint / api / spec basename) to disambiguate, returning (repo, false),
// and ("", false) when still ambiguous.
func pickProvider(client facts.Fact, matches []routeRef) (string, bool) {
	providers := map[string]bool{}
	for _, m := range matches {
		if m.repo != client.Repo {
			providers[m.repo] = true
		}
	}
	switch len(providers) {
	case 0:
		return "", false
	case 1:
		for p := range providers {
			return p, true
		}
	}
	hint := normalizeLabel(serviceHint(client))
	if hint == "" {
		return "", false // ambiguous, no hint
	}
	for p := range providers {
		if normalizeLabel(p) == hint || strings.Contains(normalizeLabel(p), hint) || strings.Contains(hint, normalizeLabel(p)) {
			return p, false
		}
	}
	return "", false
}

// handWrittenClientSources are the `source` prop values emitted by hand-written
// HTTP-client extractors, as opposed to generated OpenAPI client specs.
var handWrittenClientSources = map[string]bool{
	"ts-http-client": true, "retrofit": true, "urlsession": true,
	"ruby-http-client": true, "go-http-client": true,
}

// httpVia returns the via label for an HTTP edge derived from a client route:
// "http-client" for a hand-written client call site, "http" for an OpenAPI
// client spec (the default).
func httpVia(client facts.Fact) string {
	if handWrittenClientSources[propString(client, "source")] {
		return "http-client"
	}
	return "http"
}

// matchConfidence classifies how trustworthy an HTTP route match is. It is
// "verified" only when the client called a provider's complete server path
// (not just a trailing fragment), the provider was the sole candidate (not
// disambiguated by a name hint), and the client path carried no inferred {}
// placeholder; otherwise "probable".
func matchConfidence(clientPath, np, provider string, matches []routeRef, unambiguous bool) string {
	if !unambiguous || strings.Contains(np, "{}") {
		return "probable"
	}
	for _, m := range matches {
		if m.repo == provider && m.fullPath == clientPath {
			return "verified"
		}
	}
	return "probable"
}

// serverPaths returns the normalized paths a server route is reachable at: its
// own path and, when present, its gateway path (prefix + path).
func serverPaths(f facts.Fact) []string {
	out := []string{normalizePath(f.Name)}
	if gw := propString(f, "gateway_path"); gw != "" {
		out = append(out, normalizePath(gw))
	}
	return out
}

func serviceHint(f facts.Fact) string {
	// target_hint (derived from a wrapper-client constant or base-URL env var) is
	// the most specific provider signal, so it is consulted first.
	if h := propString(f, "target_hint"); h != "" {
		return h
	}
	if api := propString(f, "api"); api != "" {
		return api
	}
	if spec := propString(f, "spec_file"); spec != "" {
		base := filepath.Base(spec)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return ""
}

// --- signal (B): import / shared-lib references ---

func linkImports(all []facts.Fact, normToLabel map[string]string, edges map[string]*edge) {
	for _, f := range all {
		if f.Repo == "" || f.Kind == facts.KindService {
			continue
		}
		for _, rel := range f.Relations {
			if rel.Kind != facts.RelImports && rel.Kind != facts.RelDependsOn {
				continue
			}
			provider := importProvider(rel.Target, f.Repo, normToLabel)
			if provider == "" {
				continue
			}
			e := edgeFor(edges, f.Repo, provider)
			e.note("import")
			if e.imports == nil {
				e.imports = map[string]bool{}
			}
			e.imports[rel.Target] = true
		}
	}
}

// importProvider maps an import target to another loaded repo, or "" if none.
// It checks candidate identifiers from the target (the @scope, then each leading
// path segment) against the normalized repo labels, skipping self-matches.
func importProvider(target, consumer string, normToLabel map[string]string) string {
	target = strings.TrimSpace(target)
	// Skip relative / absolute filesystem imports — they are intra-repo.
	if target == "" || strings.HasPrefix(target, ".") || strings.HasPrefix(target, "/") {
		return ""
	}
	for _, cand := range importCandidates(target) {
		if label, ok := normToLabel[normalizeLabel(cand)]; ok && label != consumer {
			return label
		}
	}
	return ""
}

// importCandidates extracts the identifier tokens an import target may name a
// repo by, most-significant first: e.g. "@app-web/lib-api" ->
// ["app-web", "lib-api"], "lib-core/foo" -> ["lib-core", "foo"].
func importCandidates(target string) []string {
	t := strings.TrimPrefix(target, "@")
	parts := strings.Split(t, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- signal (C): shared symbol surface ---

// minSharedSymbols is the fewest distinct distinctive type identities two repos
// must share before a shared-symbol edge is drawn. Set above 2 so an incidental
// name collision (a `JsonParser` both repos happen to define) cannot fabricate a
// dependency, while genuinely shared/vendored code (a protocol header copied
// between repos) shares many.
const minSharedSymbols = 3

// genericTypeNames are common unqualified type names too generic to link on by
// themselves. Namespaced identities (e.g. "onelab::number") bypass this list —
// sharing a namespace across repos is itself meaningful.
var genericTypeNames = map[string]bool{
	"config": true, "error": true, "manager": true, "options": true,
	"result": true, "base": true, "impl": true, "utils": true, "util": true,
	"common": true, "exception": true, "context": true, "data": true,
	"info": true, "item": true, "node": true, "entry": true, "helper": true,
	"settings": true, "logger": true, "test": true, "main": true, "model": true,
	"request": true, "response": true, "status": true, "value": true,
}

// linkSharedSymbols connects repos that declare enough of the same distinctive
// types. The relationship is symmetric (shared/vendored code, not a one-way
// dependency), so qualifying pairs get a bidirectional pair of edges.
func linkSharedSymbols(all []facts.Fact, edges map[string]*edge) {
	repoModules := moduleNamesByRepo(all)

	// identity -> set of repos that declare a type with that identity.
	idToRepos := map[string]map[string]bool{}
	for _, f := range all {
		if f.Kind != facts.KindSymbol || f.Repo == "" || !isTypeSymbol(f) {
			continue
		}
		id := typeIdentity(f.Name, repoModules[f.Repo])
		if !isDistinctiveIdentity(id) {
			continue
		}
		if idToRepos[id] == nil {
			idToRepos[id] = map[string]bool{}
		}
		idToRepos[id][f.Repo] = true
	}

	// For each identity shared by 2+ repos, record it against every repo pair.
	// pairShared["a\x00b"] (a<b) -> set of shared identities.
	pairShared := map[string]map[string]bool{}
	for id, repos := range idToRepos {
		if len(repos) < 2 {
			continue
		}
		rs := make([]string, 0, len(repos))
		for r := range repos {
			rs = append(rs, r)
		}
		sort.Strings(rs)
		for i := 0; i < len(rs); i++ {
			for j := i + 1; j < len(rs); j++ {
				key := rs[i] + "\x00" + rs[j]
				if pairShared[key] == nil {
					pairShared[key] = map[string]bool{}
				}
				pairShared[key][id] = true
			}
		}
	}

	// Materialize a bidirectional edge for each pair over the threshold.
	for key, ids := range pairShared {
		if len(ids) < minSharedSymbols {
			continue
		}
		a, b, _ := strings.Cut(key, "\x00")
		for _, pair := range [2][2]string{{a, b}, {b, a}} {
			e := edgeFor(edges, pair[0], pair[1])
			e.note("shared_symbols")
			if e.symbols == nil {
				e.symbols = map[string]bool{}
			}
			for id := range ids {
				e.symbols[id] = true
			}
		}
	}
}

// isTypeSymbol reports whether a symbol fact is a type-like declaration (the
// portable "contract surface"), excluding functions, methods, variables, etc.
func isTypeSymbol(f facts.Fact) bool {
	switch propString(f, "symbol_kind") {
	case facts.SymbolClass, facts.SymbolStruct, facts.SymbolInterface, facts.SymbolEnum:
		return true
	}
	return false
}

// moduleNamesByRepo returns, per repo, the module (directory) names sorted
// longest-first, so the longest matching prefix can be stripped from a symbol.
func moduleNamesByRepo(all []facts.Fact) map[string][]string {
	byRepo := map[string][]string{}
	for _, f := range all {
		if f.Kind != facts.KindModule || f.Repo == "" {
			continue
		}
		byRepo[f.Repo] = append(byRepo[f.Repo], f.Name)
	}
	for r := range byRepo {
		ms := byRepo[r]
		sort.Slice(ms, func(i, j int) bool { return len(ms[i]) > len(ms[j]) })
	}
	return byRepo
}

// typeIdentity strips the repo-specific "<module>." directory prefix from a
// symbol's name, returning the portable namespace/type-qualified remainder that
// is shared across repos (e.g. "src/common.onelab::Foo" -> "onelab::Foo",
// "Common.onelab::Foo" -> "onelab::Foo"). The repo's own module names are used so
// the differing directory layouts of two repos do not defeat the match.
func typeIdentity(name string, modules []string) string {
	for _, m := range modules { // longest first
		if len(name) > len(m)+1 && strings.HasPrefix(name, m+".") {
			return name[len(m)+1:]
		}
	}
	// Fallback: strip up to the first "." when no module matched.
	if i := strings.IndexByte(name, '.'); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}

// isDistinctiveIdentity filters out identities too generic to safely link on. A
// namespaced identity (containing "::" or ".") is always kept; an unqualified one
// is kept only if it is reasonably long and not a common generic type name.
func isDistinctiveIdentity(id string) bool {
	if id == "" {
		return false
	}
	if strings.Contains(id, "::") || strings.Contains(id, ".") {
		return true
	}
	if len(id) < 5 {
		return false
	}
	return !genericTypeNames[strings.ToLower(id)]
}

// --- materialization ---

func edgeFor(edges map[string]*edge, consumer, provider string) *edge {
	key := consumer + "\x00" + provider
	e, ok := edges[key]
	if !ok {
		e = &edge{consumer: consumer, provider: provider}
		edges[key] = e
	}
	return e
}

// covFor returns the HTTP-client coverage tally for a repo, creating it on first use.
func covFor(cov map[string]*httpCoverage, repo string) *httpCoverage {
	c, ok := cov[repo]
	if !ok {
		c = &httpCoverage{}
		cov[repo] = c
	}
	return c
}

func materialize(edges map[string]*edge, allRepos []string, cov map[string]*httpCoverage) []facts.Fact {
	// Stable order over edges.
	keys := make([]string, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// providers[consumer] = sorted set of providers it depends on. Used to attach
	// the traversable depends_on relations to each consumer's service node.
	providers := map[string][]string{}
	repoSet := map[string]bool{}

	// Detailed per-pair dependency facts carry the evidence (endpoints/imports)
	// and are queryable, but hold NO relations: the traversable graph edge lives
	// on the service node, so we avoid creating a stray "a -> b" graph node.
	var depFacts []facts.Fact
	for _, k := range keys {
		e := edges[k]
		repoSet[e.consumer] = true
		repoSet[e.provider] = true
		providers[e.consumer] = append(providers[e.consumer], e.provider)

		props := map[string]any{
			"type":      "cross_repo",
			"synthetic": SyntheticMarker,
			"via":       sortedKeys(e.via),
		}
		if e.confidence != "" {
			props["confidence"] = e.confidence
		}
		if len(e.endpoints) > 0 {
			eps := sortedKeys(e.endpoints)
			props["endpoint_count"] = len(eps)
			props["endpoints"] = cap25(eps)
		}
		if len(e.imports) > 0 {
			imps := sortedKeys(e.imports)
			props["import_count"] = len(imps)
			props["import_samples"] = cap25(imps)
		}
		if len(e.symbols) > 0 {
			syms := sortedKeys(e.symbols)
			props["symbol_count"] = len(syms)
			props["symbol_samples"] = cap25(syms)
		}

		depFacts = append(depFacts, facts.Fact{
			Kind:  facts.KindDependency,
			Name:  fmt.Sprintf("%s -> %s", e.consumer, e.provider),
			Repo:  e.consumer,
			Props: props,
		})
	}

	// Every loaded repo becomes an addressable service node, even with no
	// cross-repo edges, so find_path/traverse can resolve isolated repo labels.
	for _, r := range allRepos {
		repoSet[r] = true
	}

	// Service nodes first (sorted), then dependency edges. Each consumer's
	// service node gets a depends_on relation per provider, which BuildGraph
	// turns into the cross-repo graph edge.
	repos := make([]string, 0, len(repoSet))
	for r := range repoSet {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	out := make([]facts.Fact, 0, len(repos)+len(depFacts))
	for _, r := range repos {
		var rels []facts.Relation
		for _, p := range providers[r] {
			rels = append(rels, facts.Relation{Kind: facts.RelDependsOn, Target: p})
		}
		props := map[string]any{"synthetic": SyntheticMarker}
		// Attach detected-vs-resolved coverage so a node with no outbound edges but
		// unresolved call sites reads as a coverage gap, not a true isolate. The list
		// shape (one entry per edge_type) lets future edge kinds (e.g. Kafka) slot in
		// without changing the readers.
		if c := cov[r]; c != nil && c.detected > 0 {
			props["edge_coverage"] = []map[string]any{{
				"edge_type":  "http_client",
				"detected":   c.detected,
				"resolved":   c.resolved,
				"unresolved": c.detected - c.resolved,
			}}
		}
		out = append(out, facts.Fact{
			Kind:      facts.KindService,
			Name:      r,
			Repo:      r,
			Props:     props,
			Relations: rels,
		})
	}
	out = append(out, depFacts...)
	return out
}

// --- small helpers ---

func roleOf(f facts.Fact) string { return propString(f, "role") }

func propString(f facts.Fact, key string) string {
	if f.Props == nil {
		return ""
	}
	if v, ok := f.Props[key].(string); ok {
		return v
	}
	return ""
}

func normalizeMethod(m string) string {
	m = strings.ToUpper(strings.TrimSpace(m))
	switch m {
	case "", "USE", "ALL", "MIDDLEWARE":
		return ""
	}
	return m
}

func routeKey(normPath, method string) string { return normPath + "|" + method }

// normalizePath trims a trailing slash and collapses path parameters in any
// framework syntax ({id}, :id, <id>) to a single "{}" placeholder, so a client
// path matches the server path it calls regardless of param naming.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		switch {
		case strings.HasPrefix(s, ":"):
			segs[i] = "{}"
		case strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}"):
			segs[i] = "{}"
		case strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">"):
			segs[i] = "{}"
		case strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"):
			// Next.js dynamic segments: [id], [slug], [...rest].
			segs[i] = "{}"
		}
	}
	return strings.Join(segs, "/")
}

// pathSuffixes returns every trailing-segment suffix of a normalized path that
// has at least minSharedSegments non-empty segments, longest first. Each suffix
// is rendered leading-slash-canonical ("/seg/seg/..."), so a server path
// "/api/settings/entitlements/definitions" yields:
//
//	/api/settings/entitlements/definitions
//	/settings/entitlements/definitions
//	/entitlements/definitions
//
// ("definitions" alone is dropped: below minSharedSegments). This lets a client
// calling a base-relative subpath match the server serving the full path.
func pathSuffixes(normPath string) []string {
	var segs []string
	for _, s := range strings.Split(normPath, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	var out []string
	for start := 0; start+minSharedSegments <= len(segs); start++ {
		out = append(out, "/"+strings.Join(segs[start:], "/"))
	}
	return out
}

// lookupClientMatches resolves a client path against the server suffix index by
// trying the client path's own trailing-segment suffixes longest-first and
// returning the first (most specific) hit, plus the suffix that matched. The
// longest suffix is the full client path, so an exact full-path match still wins
// first; shorter suffixes then let a prefixed client path ("/api/settings/x/y")
// match a server serving the un-prefixed path ("/x/y"). For sub-minSharedSegments
// paths (no suffixes) it falls back to a single full-path lookup, preserving the
// original behavior.
func lookupClientMatches(server map[string][]routeRef, clientPath, method string) ([]routeRef, string) {
	sufs := pathSuffixes(clientPath)
	if len(sufs) == 0 {
		return server[routeKey(clientPath, method)], clientPath
	}
	for _, suf := range sufs {
		if m := server[routeKey(suf, method)]; len(m) > 0 {
			return m, suf
		}
	}
	return nil, clientPath
}

// canonicalLeadingSlash ensures a non-empty path starts with "/", so a
// base-relative client path ("settings/x") compares equal to the indexed
// suffix form ("/settings/x").
func canonicalLeadingSlash(p string) string {
	if p == "" || strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

// isGenericPath reports whether a path is too low-signal to safely link on
// (e.g. /health, /status) — no path parameter and fewer than two segments.
func isGenericPath(normPath string) bool {
	var segs []string
	for _, s := range strings.Split(normPath, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if strings.Contains(normPath, "{}") {
		return false
	}
	return len(segs) < 2
}

// normalizeLabel lowercases and strips '-'/'_' so "app-web",
// "app_web", and "AppWeb" all compare equal.
func normalizeLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func cap25(ss []string) []string {
	if len(ss) > maxSamples {
		return ss[:maxSamples]
	}
	return ss
}
