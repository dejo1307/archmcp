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
	external int // detected call sites to a hardcoded external host — not an internal blind spot
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

// indexServerRoutes builds the suffix+method -> server route index the HTTP linker
// matches client calls against: every trailing-segment suffix of every server
// route's normalized path(s), keyed by routeKey. Shared by linkHTTP and
// UnmatchedClientRouteKeys so both resolve a client call identically. serverPaths
// already returns normalized paths; fullPath records which full path a suffix came
// from, so a match can tell a full-path hit from a fragment hit.
func indexServerRoutes(all []facts.Fact) map[string][]routeRef {
	server := map[string][]routeRef{}
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" || roleOf(f) == "client" {
			continue
		}
		method := normalizeMethod(propString(f, "method"))
		if method == "" {
			continue
		}
		for _, p := range serverPaths(f) {
			ref := routeRef{repo: f.Repo, method: method, path: f.Name, fullPath: p}
			for _, suf := range pathSuffixes(p) {
				server[routeKey(suf, method)] = append(server[routeKey(suf, method)], ref)
			}
		}
	}
	return server
}

func linkHTTP(all []facts.Fact, edges map[string]*edge, cov map[string]*httpCoverage) {
	// Index server routes by normalized path-suffix + method (shared with the
	// unmatched-client pass so verdicts stay in lockstep).
	server := indexServerRoutes(all)

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
		// A call to a hardcoded external host (e.g. a third-party API) can never
		// resolve to a loaded repo, so bucket it separately instead of leaving it in
		// unresolved — otherwise it reads as an internal blind spot it is not.
		if isExternalClient(f) {
			covFor(cov, f.Repo).external++
			continue
		}
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
	"swift-endpoint": true, "ruby-http-client": true, "go-http-client": true,
	"php-http-client": true, "ts-grpc-client": true, "go-grpc-client": true,
	"python-grpc-client": true,
}

// httpVia returns the via label for an HTTP edge derived from a client route:
// "grpc" for a gRPC call site, "http-client" for a hand-written HTTP client call
// site, "http" for an OpenAPI client spec (the default).
func httpVia(client facts.Fact) string {
	if propString(client, "framework") == "grpc" {
		return "grpc"
	}
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

// --- server-side inverse: routes no loaded client calls ---

// RouteIdentity returns the stable identity key of a route fact — its repo, HTTP
// method (normalized the same way the matcher normalizes), and path (the fact
// Name). The keys in the set returned by UnmatchedServerRouteKeys use this exact
// form, so a caller can flag the matching route facts without re-deriving any
// path or method normalization of its own.
func RouteIdentity(f facts.Fact) string {
	return routeIdentityKey(f.Repo, normalizeMethod(propString(f, "method")), f.Name)
}

func routeIdentityKey(repo, method, path string) string {
	return repo + "\x00" + method + "\x00" + path
}

// UnmatchedServerRouteKeys returns the identities (see RouteIdentity) of server
// routes that no loaded client route resolves to — endpoints unused by every
// client in the snapshot. It reuses the cross-repo HTTP linker's exact server
// index and suffix/method matching, so a route flagged here is precisely one the
// linker found no caller for; only the verdict differs from linkHTTP.
//
// Matching is deliberately generous: a server route counts as used on any
// suffix+method hit, regardless of match confidence or which repo the caller is
// in. This biases the unused set toward false negatives — it will never flag a
// route that shows any sign of use, which is the safe direction when the output
// may drive endpoint removal. Returns nil for single-repo snapshots: with no
// other repo loaded there are no clients for a route to be unused by.
func UnmatchedServerRouteKeys(all []facts.Fact) map[string]bool {
	if len(repoLabelLookup(all)) < 2 {
		return nil
	}

	// Index server routes by normalized path-suffix + method, exactly as linkHTTP
	// does, while recording every distinct server route identity so the un-hit
	// ones can be reported afterwards.
	server := map[string][]routeRef{}
	identities := map[string]bool{}
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" || roleOf(f) == "client" {
			continue
		}
		method := normalizeMethod(propString(f, "method"))
		if method == "" {
			continue
		}
		// Skip low-signal generic paths (/health, /status, single-segment routes):
		// the matcher refuses to link these (a client call to one is dropped by the
		// same isGenericPath filter below), so we cannot reliably tell whether a
		// client uses them — and infra / non-client callers commonly do. Excluding
		// them from the candidate set keeps the unused verdict to routes we can
		// actually reason about, never flagging a generic endpoint that may be in use.
		if isGenericPath(normalizePath(f.Name)) {
			continue
		}
		identities[routeIdentityKey(f.Repo, method, f.Name)] = true
		for _, p := range serverPaths(f) {
			ref := routeRef{repo: f.Repo, method: method, path: f.Name, fullPath: p}
			for _, suf := range pathSuffixes(p) {
				server[routeKey(suf, method)] = append(server[routeKey(suf, method)], ref)
			}
		}
	}

	// Mark every server route any client resolves to (by suffix + method) as used,
	// and record which repos actually serve a cross-repo client (HTTP providers).
	matched := map[string]bool{}
	providerRepos := map[string]bool{}
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" || roleOf(f) != "client" {
			continue
		}
		method := normalizeMethod(propString(f, "method"))
		if method == "" {
			continue
		}
		np := normalizePath(f.Name)
		if isGenericPath(np) {
			continue
		}
		matches, _ := lookupClientMatches(server, canonicalLeadingSlash(np), method)
		for _, m := range matches {
			matched[routeIdentityKey(m.repo, m.method, m.path)] = true
			if m.repo != f.Repo {
				providerRepos[m.repo] = true
			}
		}
	}

	// Only a repo that serves at least one cross-repo client is an HTTP provider
	// for which "unused by clients" is meaningful. A pure consumer or leaf repo (a
	// frontend's own page routes, a mobile app) has no clients among the loaded
	// repos, so flagging its routes would be vacuous noise — skip it, the same way
	// a single-repo snapshot is skipped, applied per repo.
	unmatched := map[string]bool{}
	for id := range identities {
		if matched[id] || !providerRepos[repoFromIdentity(id)] {
			continue
		}
		unmatched[id] = true
	}
	return unmatched
}

// UnmatchedClientRouteKeys returns the identity (see RouteIdentity) of every client
// route the cross-repo HTTP linker could not resolve to a loaded server route,
// mapped to a short reason: "no_method" (the call site carried no usable verb),
// "generic_path" (a sub-2-segment path the matcher deliberately skips), or
// "no_match" (no server route shares a >=2-segment suffix + method). It mirrors
// linkHTTP's exact resolution steps, so the set is precisely the client calls that
// fell into the unresolved coverage count — the queryable counterpart to the
// aggregate edge_coverage numbers. External calls (hardcoded third-party hosts) are
// expected non-matches and are omitted. Returns nil for single-repo snapshots.
func UnmatchedClientRouteKeys(all []facts.Fact) map[string]string {
	if len(repoLabelLookup(all)) < 2 {
		return nil
	}
	server := indexServerRoutes(all)
	unmatched := map[string]string{}
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" || roleOf(f) != "client" {
			continue
		}
		if isExternalClient(f) {
			continue // a hardcoded external host is an expected non-match, not a blind spot
		}
		id := RouteIdentity(f)
		method := normalizeMethod(propString(f, "method"))
		if method == "" {
			unmatched[id] = "no_method"
			continue
		}
		np := normalizePath(f.Name)
		if isGenericPath(np) {
			unmatched[id] = "generic_path"
			continue
		}
		matches, _ := lookupClientMatches(server, canonicalLeadingSlash(np), method)
		if provider, _ := pickProvider(f, matches); provider == "" {
			unmatched[id] = "no_match"
		}
	}
	return unmatched
}

// repoFromIdentity extracts the repo label from a route identity key (see
// routeIdentityKey, which prefixes the repo before the first NUL separator).
func repoFromIdentity(id string) string {
	if i := strings.IndexByte(id, '\x00'); i >= 0 {
		return id[:i]
	}
	return id
}

// --- signal (B): import / shared-lib references ---

func linkImports(all []facts.Fact, normToLabel map[string]string, edges map[string]*edge) {
	ownDirs := repoTopDirs(all)
	for _, f := range all {
		if f.Repo == "" || f.Kind == facts.KindService {
			continue
		}
		for _, rel := range f.Relations {
			if rel.Kind != facts.RelImports && rel.Kind != facts.RelDependsOn {
				continue
			}
			provider := importProvider(rel.Target, f.Repo, normToLabel, ownDirs[f.Repo])
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
//
// ownDirs is the consumer repo's own top-level source directories. A target rooted
// at one of them is an intra-repo file/module reference whose interior path
// segments may coincide with another repo's short label (e.g. a "com/acme/app/…"
// package path vs a backend repo labeled "acme"), so it is skipped before any
// candidate matching — this is what keeps a repo's own files from fabricating a
// cross-repo edge, while still allowing genuine deep import paths (e.g. a Go
// "github.com/org/repo/pkg", whose leading "github.com" is not a source dir).
func importProvider(target, consumer string, normToLabel map[string]string, ownDirs map[string]bool) string {
	target = strings.TrimSpace(target)
	// Skip relative / absolute filesystem imports — they are intra-repo.
	if target == "" || strings.HasPrefix(target, ".") || strings.HasPrefix(target, "/") {
		return ""
	}
	if head := leadingSegment(target); head != "" && ownDirs[normalizeLabel(head)] {
		return "" // intra-repo self-reference, not a cross-repo dependency
	}
	for _, cand := range importCandidates(target) {
		if label, ok := normToLabel[normalizeLabel(cand)]; ok && label != consumer {
			return label
		}
	}
	return ""
}

// leadingSegment returns the first non-empty path segment of an import target or
// module name, ignoring a leading "@" scope marker (so "@app-web/lib" -> "app-web",
// "com/acme/app" -> "com", "acme" -> "acme").
func leadingSegment(target string) string {
	for _, p := range strings.Split(strings.TrimPrefix(target, "@"), "/") {
		if p != "" {
			return p
		}
	}
	return ""
}

// repoTopDirs returns, per repo, the set of normalized leading path segments of
// its module names — the repo's own top-level source roots. importProvider uses it
// to recognize an import target as an intra-repo reference.
func repoTopDirs(all []facts.Fact) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, f := range all {
		if f.Kind != facts.KindModule || f.Repo == "" {
			continue
		}
		head := leadingSegment(f.Name)
		if head == "" {
			continue
		}
		if out[f.Repo] == nil {
			out[f.Repo] = map[string]bool{}
		}
		out[f.Repo][normalizeLabel(head)] = true
	}
	return out
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
	repoLang := primaryLanguageByRepo(all)

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

	// For each identity shared by 2+ repos, record it against every repo pair — but
	// only when the shared identity is a trustworthy coupling signal for that pair.
	// A namespace-qualified identity (contains "::"/".", the mark of vendored/shared
	// source) always counts, language-independent. A bare unqualified name counts
	// only between same-language repos: two apps written in different languages
	// sharing a plain domain type name (e.g. Kotlin and Swift both declaring
	// "LoginViewModel") is parallel modeling of the same product, not shared code,
	// and must not fabricate a dependency.
	// pairShared["a\x00b"] (a<b) -> set of shared identities.
	pairShared := map[string]map[string]bool{}
	for id, repos := range idToRepos {
		if len(repos) < 2 {
			continue
		}
		qualified := isQualifiedIdentity(id)
		rs := make([]string, 0, len(repos))
		for r := range repos {
			rs = append(rs, r)
		}
		sort.Strings(rs)
		for i := 0; i < len(rs); i++ {
			for j := i + 1; j < len(rs); j++ {
				if !qualified && repoLang[rs[i]] != repoLang[rs[j]] {
					continue
				}
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
	if isQualifiedIdentity(id) {
		return true
	}
	if len(id) < 5 {
		return false
	}
	return !genericTypeNames[strings.ToLower(id)]
}

// isQualifiedIdentity reports whether a type identity is namespace-qualified
// (contains "::" or "."). A qualified identity shared across repos is a strong
// vendored/shared-source signal, independent of language; an unqualified one is a
// bare type name that two repos may coincidentally share.
func isQualifiedIdentity(id string) bool {
	return strings.Contains(id, "::") || strings.Contains(id, ".")
}

// primaryLanguageByRepo returns each repo's dominant source language (the most
// common `language` prop across its facts), or "" when unknown. Used to gate
// unqualified shared-symbol links to same-language repo pairs, so two apps in
// different languages sharing a plain domain type name are not linked.
func primaryLanguageByRepo(all []facts.Fact) map[string]string {
	counts := map[string]map[string]int{}
	for _, f := range all {
		if f.Repo == "" {
			continue
		}
		lang := propString(f, "language")
		if lang == "" {
			continue
		}
		if counts[f.Repo] == nil {
			counts[f.Repo] = map[string]int{}
		}
		counts[f.Repo][lang]++
	}
	out := map[string]string{}
	for repo, langs := range counts {
		best, bestN := "", -1
		for l, n := range langs {
			if n > bestN || (n == bestN && l < best) {
				best, bestN = l, n
			}
		}
		out[repo] = best
	}
	return out
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
				"external":   c.external,
				"unresolved": c.detected - c.resolved - c.external,
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

// isExternalClient reports whether a client route targets a hardcoded external host
// (tagged external=true by the extractor). Tolerates the bool surviving a JSON
// round-trip as a bool literal.
func isExternalClient(f facts.Fact) bool {
	if f.Props == nil {
		return false
	}
	v, _ := f.Props["external"].(bool)
	return v
}

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
	case "", "USE", "ALL", "MIDDLEWARE", "DRAW":
		// DRAW is a Rails route-delegation placeholder (config/routes.rb draw(:pkg)),
		// not a real HTTP method — inert for matching and unused-route flagging.
		return ""
	}
	return m
}

func routeKey(normPath, method string) string { return normPath + "|" + method }

// formatExtensions are response-format suffixes a client may append to a path
// (Rails renders these as an optional ".:format" that never appears in the route
// path) — so they must be stripped before comparing a client call to a server route.
var formatExtensions = []string{".json", ".xml"}

// stripFormatExtension removes a trailing response-format extension from a single
// path segment: "orders.json" -> "orders", "{id}.json" -> "{id}" (so the later
// {}-collapse still fires). Only the known format suffixes are stripped, so a
// version-like segment ("v2.5") or a real dotted name is left intact.
func stripFormatExtension(seg string) string {
	for _, ext := range formatExtensions {
		if len(seg) > len(ext) && strings.HasSuffix(seg, ext) {
			return seg[:len(seg)-len(ext)]
		}
	}
	return seg
}

// normalizePath trims a trailing slash, strips a trailing response-format extension
// (.json/.xml) from the final segment, and collapses path parameters in any
// framework syntax ({id}, :id, <id>) to a single "{}" placeholder, so a client
// path matches the server path it calls regardless of param naming or format suffix.
func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	segs := strings.Split(p, "/")
	// Strip a format extension from the final segment before param collapse, so a
	// client call "/devices/{id}.json" normalizes to the same "/devices/{}" the
	// server route "/devices/:id" produces.
	if n := len(segs); n > 0 {
		segs[n-1] = stripFormatExtension(segs[n-1])
	}
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
