// Package http links repos by HTTP/gRPC route role matching: a route one repo CALLS
// (role=client) resolving to a route another repo SERVES.
//
// It also owns the two inverse passes — which server routes no client calls, and which
// client calls resolve to no server. They live here rather than beside the linker
// because they must apply byte-identical matching rules; when they were separate
// helpers, "must stay in lockstep with linkHTTP" was a comment repeated three times.
package http

import (
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/crossrepo/routeindex"
	"github.com/enola-labs/enola/internal/linkers/vocab"
	"github.com/enola-labs/enola/pkg/plugin"
)

// CoverageEdgeType is the edge class this signal reports coverage under.
const CoverageEdgeType = "http_client"

// Signal matches client call sites against server routes across repos.
type Signal struct {
	m *routeindex.Matcher
}

// New returns the signal, matching under the given vocabulary.
func New(v *vocab.Set) *Signal { return &Signal{m: routeindex.New(v)} }

func (s *Signal) Name() string { return "http" }

func (s *Signal) Phase() plugin.SignalPhase { return plugin.PhaseDirectional }

// --- signal (A): HTTP route role matching ---

func (s *Signal) Contribute(in plugin.SignalInput, out plugin.EvidenceSink) {
	m := s.m
	// Index server routes by normalized path-suffix + method (shared with the
	// unmatched-client pass so verdicts stay in lockstep).
	all := in.Facts()
	server := m.IndexServerRoutes(all)

	// Match client routes against the server index.
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" || routeindex.RoleOf(f) != facts.RoleClient ||
			f.PropString(facts.PropRouteType) == facts.RouteTypeGraphQL {
			continue
		}
		// Every client call site is a detected outbound edge. Counting here, before
		// the low-signal filters below, means call sites we choose not to resolve
		// (no method, generic path) and call sites with no matching server both fall
		// into unresolved (detected - resolved) — the blind spot the report exposes.
		out.Coverage(f.Repo, CoverageEdgeType).Detected++
		// Attempt to resolve the call site to a loaded server first, then fall back
		// to the external bucket. Ordering matters: a route tagged external may
		// still target a hardcoded *internal* host that is loaded (the Go extractor
		// tags those since v101), and such a call must keep its cross-repo edge
		// rather than vanish into the external bucket. Bucketing external only after
		// a failed match also preserves the blind-spot signal — an untagged,
		// unmatched call still falls into unresolved (detected - resolved - external).
		matched := false
		method := routeindex.NormalizeMethod(f.PropString("method"))
		if method != "" {
			np := m.NormalizePath(f.Name)
			if !m.IsGenericPath(np) {
				// Canonicalize the leading slash so a base-relative client path
				// ("settings/x") matches the indexed suffix form ("/settings/x").
				clientPath := routeindex.CanonicalLeadingSlash(np)
				// Try the client path's trailing-segment suffixes against the server
				// suffix index, longest first. The server index already holds suffixes
				// of every server path, so matching client suffixes too makes the join
				// symmetric: it resolves a client call that carries an extra gateway/BFF
				// prefix ("/api/settings/tickets/{}/resolve") to a server serving the
				// un-prefixed path ("/tickets/{}/resolve"), as well as the reverse (a
				// base-relative client calling a longer server path).
				matches, matchedPath := m.LookupClientMatches(server, clientPath, method)
				provider, unambiguous := pickProvider(f, matches)
				// A single-segment path (/activate) cleared the generic vocabulary but
				// is thinner evidence than a multi-segment one: there is less path to
				// coincide by accident, so a hint-disambiguated pick among several
				// candidate providers is normally not enough. Demand an outright
				// unambiguous match — with one carve-out: a hint whose normalized form
				// EQUALS a provider's label exactly is not a coincidence class (the
				// source names the host — `${config.ACME_HOST}/mcp` — and two
				// loaded repos serving /mcp is precisely the case that hint exists
				// for). Substring hint matches stay rejected for these paths.
				//
				// Only target_hint qualifies, for the same reason the external
				// classification below says so: serviceHint falls back to the `api`
				// prop, which is the client FILE's name. A file named api.ts would
				// otherwise elect the repo named api out of several candidates, and
				// renaming that file would move the dependency.
				if provider != "" && routeindex.SingleSegmentPath(np) && !unambiguous &&
					facts.NormalizeRepoLabel(f.PropString("target_hint")) != facts.NormalizeRepoLabel(provider) {
					provider = ""
				}
				// A non-empty provider means the call site matched a loaded service (a
				// self-match is internal, not a blind spot) — count it resolved either way.
				if provider != "" {
					out.Coverage(f.Repo, CoverageEdgeType).Resolved++
					matched = true
				}
				if provider != "" && provider != f.Repo {
					e := out.Edge(f.Repo, provider)
					e.Via(httpVia(f))
					e.Sample(plugin.BucketEndpoints, method+" "+f.Name)
					e.Confidence(matchConfidence(matchedPath, np, provider, matches, unambiguous))
				}
			}
		}
		// A call to a hardcoded external host (e.g. a third-party API) that matched
		// no loaded repo is bucketed separately instead of left in unresolved —
		// otherwise it reads as an internal blind spot it is not. A HINTED
		// external — the call carries a host-derived target_hint that resolves to
		// no loaded repo (fastly, sentry, an opensearch cluster) — is the same
		// expected non-match: the source names where the call goes, and it is not
		// here. Only target_hint qualifies; the api fallback is a file name, not
		// a host, and must never classify a real internal call as external.
		if !matched && routeindex.IsExternalClient(f) {
			out.Coverage(f.Repo, CoverageEdgeType).External++
			continue
		}
		if !matched {
			if hint := f.PropString("target_hint"); hint != "" {
				if _, loaded := in.ResolveRepo(hint); !loaded {
					out.Coverage(f.Repo, CoverageEdgeType).External++
					continue
				}
			}
			// Attribution by declared intent: when the calling repo's declaration
			// names exactly one http-client seam, an unmatched call is attributed
			// to that declared target — counted in its own bucket, never as a
			// resolved edge endpoint. The declaration is stated, the attribution
			// is labeled, and the blind spot stops masquerading as unknown.
			if target, one := soleDeclaredHTTPTarget(in.Facts(), f.Repo); one && target != f.Repo {
				out.Coverage(f.Repo, CoverageEdgeType).Declared++
			}
		}
	}
}

// resolveRepoIn mirrors SignalInput.ResolveRepo for the fact-set-only callers:
// a candidate resolves when its normalized form equals a loaded repo label's.
func resolveRepoIn(all []facts.Fact, candidate string) (string, bool) {
	want := facts.NormalizeRepoLabel(candidate)
	if want == "" {
		return "", false
	}
	for r := range reposOf(all) {
		if facts.NormalizeRepoLabel(r) == want {
			return r, true
		}
	}
	return "", false
}

// soleDeclaredHTTPTarget returns the single repo an intent declaration says
// this repo consumes via http-client, and whether exactly one is declared.
func soleDeclaredHTTPTarget(all []facts.Fact, repo string) (string, bool) {
	target := ""
	n := 0
	for _, f := range all {
		if f.Kind != facts.KindIntent || f.Repo != repo ||
			f.PropString("intent_kind") != "consumes" ||
			f.PropString("via") != facts.ViaHTTPClient {
			continue
		}
		n++
		target = f.PropString("target")
	}
	return target, n == 1
}

// pickProvider resolves which provider repo a client route points at, and
// whether that resolution was unambiguous. With a single candidate repo it
// returns (repo, true); with several it uses the client's service hint
// (target_hint / api / spec basename) to disambiguate, returning (repo, false),
// and ("", false) when still ambiguous.
//
// A call its OWN repo serves resolves to that repo: the nearest explanation for
// "this frontend calls /v1/search" is the backend sitting beside it, not an
// API-compatible reimplementation in some other loaded repo. Returning the self
// repo counts the call resolved for coverage while the caller's provider !=
// f.Repo guard keeps it from drawing an edge. Without this, a repo that both
// serves and calls a path is the one candidate that can never win, and any repo
// whose own routes extract thinly hands its whole client surface to a neighbour.
// The trade-off is a genuine BFF that proxies a path it also serves: its edge is
// dropped — a miss, in a linker that everywhere prefers missing to fabricating.
func pickProvider(client facts.Fact, matches []routeindex.RouteRef) (string, bool) {
	providers := map[string]bool{}
	for _, m := range matches {
		if m.Repo == client.Repo {
			return client.Repo, true
		}
		providers[m.Repo] = true
	}
	switch len(providers) {
	case 0:
		return "", false
	case 1:
		for p := range providers {
			return p, true
		}
	}
	hint := facts.NormalizeRepoLabel(serviceHint(client))
	if hint == "" {
		return "", false // ambiguous, no hint
	}
	for p := range providers {
		if facts.NormalizeRepoLabel(p) == hint || strings.Contains(facts.NormalizeRepoLabel(p), hint) || strings.Contains(hint, facts.NormalizeRepoLabel(p)) {
			return p, false
		}
	}
	return "", false
}

// httpVia returns the via label for an HTTP edge derived from a client route:
// "grpc" for a gRPC call site, "http-client" for a hand-written HTTP client call
// site, "http" for an OpenAPI client spec (the default).
//
// The hand-written set is facts.HandWrittenClientSources, declared beside the
// RouteSource constants the extractors emit. It used to be a private copy here, which
// is precisely how it came to omit the two Java sources for as long as the Java
// HTTP-client extractor existed: nothing tied this reader to those writers.
func httpVia(client facts.Fact) string {
	if client.PropString(facts.PropFramework) == facts.FrameworkGRPC {
		return facts.ViaGRPC
	}
	if facts.HandWrittenClientSources[client.PropString(facts.PropSource)] {
		return facts.ViaHTTPClient
	}
	return facts.ViaHTTP
}

// matchConfidence classifies how trustworthy an HTTP route match is. It is
// "verified" only when the client called a provider's complete server path
// (not just a trailing fragment), the provider was the sole candidate (not
// disambiguated by a name hint), and the client path carried no inferred {}
// placeholder; otherwise "probable".
func matchConfidence(clientPath, np, provider string, matches []routeindex.RouteRef, unambiguous bool) string {
	if !unambiguous || strings.Contains(np, "{}") {
		return "probable"
	}
	for _, m := range matches {
		if m.Repo == provider && m.FullPath == clientPath {
			return "verified"
		}
	}
	return "probable"
}

func serviceHint(f facts.Fact) string {
	// target_hint (derived from a wrapper-client constant or base-URL env var) is
	// the most specific provider signal, so it is consulted first.
	if h := f.PropString("target_hint"); h != "" {
		return h
	}
	if api := f.PropString("api"); api != "" {
		return api
	}
	if spec := f.PropString("spec_file"); spec != "" {
		base := filepath.Base(spec)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return ""
}

// --- server-side inverse: routes no loaded client calls ---

// other repo loaded there are no clients for a route to be unused by.
func UnmatchedServerRouteKeys(m *routeindex.Matcher, all []facts.Fact) map[string]bool {
	if len(reposOf(all)) < 2 {
		return nil
	}

	// Index server routes by normalized path-suffix + method, exactly as linkHTTP
	// does, while recording every distinct server route identity so the un-hit
	// ones can be reported afterwards.
	server := map[string][]routeindex.RouteRef{}
	identities := map[string]bool{}
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" || routeindex.RoleOf(f) == facts.RoleClient ||
			routeindex.IsUIRoute(f) || f.PropString(facts.PropRouteType) == facts.RouteTypeGraphQL {
			continue
		}
		method := routeindex.NormalizeMethod(f.PropString("method"))
		if method == "" {
			continue
		}
		// Skip low-signal generic paths (/health, /status, /metrics): the matcher
		// refuses to link these (a client call to one is dropped by the same
		// routeindex.IsGenericPath filter below), so we cannot reliably tell whether a client
		// uses them — and infra / non-client callers commonly do. Excluding them
		// from the candidate set keeps the unused verdict to routes we can actually
		// reason about, never flagging a generic endpoint that may be in use.
		// This is a vocabulary test, not a segment count, so a named single-segment
		// route (/activate) does enter the candidate set — it is linkable, and its
		// used/unused verdict is therefore meaningful.
		if m.IsGenericPath(m.NormalizePath(f.Name)) {
			continue
		}
		identities[routeindex.RouteIdentityKey(f.Repo, method, f.Name)] = true
		for _, p := range m.ServerPaths(f) {
			ref := routeindex.RouteRef{Repo: f.Repo, Method: method, Path: f.Name, FullPath: p}
			for _, suf := range m.PathMatchKeys(p) {
				server[routeindex.RouteKey(suf, method)] = append(server[routeindex.RouteKey(suf, method)], ref)
			}
		}
	}

	// Mark every server route any client resolves to (by suffix + method) as used,
	// and record which repos actually serve a cross-repo client (HTTP providers).
	matched := map[string]bool{}
	providerRepos := map[string]bool{}
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" || routeindex.RoleOf(f) != facts.RoleClient {
			continue
		}
		method := routeindex.NormalizeMethod(f.PropString("method"))
		if method == "" {
			continue
		}
		np := m.NormalizePath(f.Name)
		if m.IsGenericPath(np) {
			continue
		}
		matches, _ := m.LookupClientMatches(server, routeindex.CanonicalLeadingSlash(np), method)
		for _, m := range matches {
			matched[routeindex.RouteIdentityKey(m.Repo, m.Method, m.Path)] = true
			if m.Repo != f.Repo {
				providerRepos[m.Repo] = true
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
		if matched[id] || !providerRepos[routeindex.RepoFromIdentity(id)] {
			continue
		}
		unmatched[id] = true
	}
	return unmatched
}

// The Reason* constants are the exhaustive set of values written to a client route's
// "unmatched_reason" prop by UnmatchedClientRouteKeys (surfaced via
// query_facts(kind=route, prop=unmatched_reason)). They are the string source of
// truth: the doc comments on UnmatchedClientRouteKeys and Engine.flagUnmatchedRoutes
// name them rather than restating the literals, so the value set cannot be described
// in two files and silently drift. Changing a value here changes emitted facts.
const (
	ReasonNoMethod = "no_method" // the call site carried no usable HTTP verb
	// ReasonDeclaredTarget marks a call that matched no server route but whose
	// repo declares exactly one http-client seam: attributed there by intent,
	// labeled as such, never resolved into an edge.
	ReasonDeclaredTarget = "attributed_by_intent"
	ReasonGenericPath    = "generic_path"    // a sub-2-segment path the matcher deliberately skips
	ReasonMethodMismatch = "method_mismatch" // a server route serves this path suffix, but not this verb
	ReasonPathUnknown    = "path_unknown"    // no server route shares a >=2-segment suffix with this path
)

// UnmatchedClientRouteKeys returns the identity (see routeindex.RouteIdentity) of every client
// route the cross-repo HTTP linker could not resolve to a loaded server route,
// mapped to one of the Reason* constants: ReasonNoMethod, ReasonGenericPath,
// ReasonMethodMismatch (a server serves this path suffix, but not this verb), or
// ReasonPathUnknown (no server shares a >=2-segment suffix with this path). It mirrors
// linkHTTP's exact resolution steps, so the set is precisely the client calls that
// fell into the unresolved coverage count — the queryable counterpart to the
// aggregate edge_coverage numbers. External calls (hardcoded third-party hosts) are
// expected non-matches and are omitted. Returns nil for single-repo snapshots.
func UnmatchedClientRouteKeys(m *routeindex.Matcher, all []facts.Fact) map[string]string {
	if len(reposOf(all)) < 2 {
		return nil
	}
	server := m.IndexServerRoutes(all)
	serverSuffixes := m.IndexServerPathSuffixes(all)
	unmatched := map[string]string{}
	for _, f := range all {
		if f.Kind != facts.KindRoute || f.Repo == "" || routeindex.RoleOf(f) != facts.RoleClient {
			continue
		}
		if routeindex.IsExternalClient(f) {
			continue // a hardcoded external host is an expected non-match, not a blind spot
		}
		if f.PropString(facts.PropRouteType) == facts.RouteTypeGraphQL {
			continue // GraphQL operations are the graphql signal's domain; an HTTP
			// verb-and-path reason stamped on one is noise, not triage
		}
		id := routeindex.RouteIdentity(f)
		method := routeindex.NormalizeMethod(f.PropString("method"))
		if method == "" {
			unmatched[id] = ReasonNoMethod
			continue
		}
		np := m.NormalizePath(f.Name)
		if m.IsGenericPath(np) {
			unmatched[id] = ReasonGenericPath
			continue
		}
		cp := routeindex.CanonicalLeadingSlash(np)
		matches, _ := m.LookupClientMatches(server, cp, method)
		if provider, _ := pickProvider(f, matches); provider == "" {
			if hint := f.PropString("target_hint"); hint != "" {
				if _, loaded := resolveRepoIn(all, hint); !loaded {
					continue // hinted external: an expected non-match, mirrored from linkHTTP
				}
			}
			if target, one := soleDeclaredHTTPTarget(all, f.Repo); one && target != f.Repo {
				unmatched[id] = ReasonDeclaredTarget
				continue
			}
			// Distinguish "a server serves this path but not this verb" from "no
			// server serves this path at all", so the residual is self-triaging.
			if m.ClientPathHasServer(serverSuffixes, cp) {
				unmatched[id] = ReasonMethodMismatch
			} else {
				unmatched[id] = ReasonPathUnknown
			}
		}
	}
	return unmatched
}

// reposOf returns the number of distinct repo labels in a fact set. The unmatched
// passes are meaningless below two: with no other repo loaded there are no clients for
// a route to be unused by.
func reposOf(all []facts.Fact) map[string]bool {
	out := map[string]bool{}
	for _, f := range all {
		if f.Repo != "" {
			out[f.Repo] = true
		}
	}
	return out
}
