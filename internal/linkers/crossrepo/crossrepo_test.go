package crossrepo

import (
	"reflect"
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// --- helpers ---

func serverRoute(repo, path, method string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindRoute,
		Name: path,
		Repo: repo,
		Props: map[string]any{
			"method": method,
			"role":   "server",
		},
	}
}

func clientRoute(repo, path, method string, extra map[string]any) facts.Fact {
	props := map[string]any{"method": method, "role": "client"}
	for k, v := range extra {
		props[k] = v
	}
	return facts.Fact{Kind: facts.KindRoute, Name: path, Repo: repo, Props: props}
}

func importDep(repo, target string) facts.Fact {
	return facts.Fact{
		Kind:      facts.KindDependency,
		Name:      repo + " -> " + target,
		Repo:      repo,
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: target}},
	}
}

// findEdge returns the cross-repo dependency (evidence) fact for
// consumer->provider, or nil.
func findEdge(out []facts.Fact, consumer, provider string) *facts.Fact {
	want := consumer + " -> " + provider
	for i := range out {
		f := out[i]
		if f.Kind == facts.KindDependency && f.Repo == consumer && f.Name == want {
			return &out[i]
		}
	}
	return nil
}

// hasServiceEdge reports whether the consumer's service node carries a
// depends_on relation to provider (the traversable graph edge).
func hasServiceEdge(out []facts.Fact, consumer, provider string) bool {
	for _, f := range out {
		if f.Kind != facts.KindService || f.Name != consumer {
			continue
		}
		for _, rel := range f.Relations {
			if rel.Kind == facts.RelDependsOn && rel.Target == provider {
				return true
			}
		}
	}
	return false
}

func serviceNodes(out []facts.Fact) []string {
	var names []string
	for _, f := range out {
		if f.Kind == facts.KindService {
			names = append(names, f.Name)
		}
	}
	sort.Strings(names)
	return names
}

// crossRepoEdges returns the consumer->provider dependency (evidence) facts —
// the actual cross-repo edges. Service nodes (which now exist for every loaded
// repo) are excluded, so this measures "did a link form?".
func crossRepoEdges(out []facts.Fact) []facts.Fact {
	var edges []facts.Fact
	for _, f := range out {
		if f.Kind == facts.KindDependency {
			edges = append(edges, f)
		}
	}
	return edges
}

// httpCoverageOf returns the http_client coverage counts attached to a service
// node, and whether an edge_coverage entry was present.
func httpCoverageOf(out []facts.Fact, repo string) (detected, resolved, unresolved int, ok bool) {
	for _, f := range out {
		if f.Kind != facts.KindService || f.Name != repo {
			continue
		}
		list, _ := f.Props["edge_coverage"].([]map[string]any)
		for _, ec := range list {
			if ec["edge_type"] != "http_client" {
				continue
			}
			return ec["detected"].(int), ec["resolved"].(int), ec["unresolved"].(int), true
		}
	}
	return 0, 0, 0, false
}

// --- normalization ---

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"/api/items/{id}":         "/api/items/{}",
		"/api/items/:id":          "/api/items/{}",
		"/api/items/<id>":         "/api/items/{}",
		"/api/items/[id]":         "/api/items/{}",
		"/api/items/":             "/api/items",
		"/api/items":              "/api/items",
		"/":                       "/",
		"/users/{uid}/pets/{pid}": "/users/{}/pets/{}",
		// Response-format suffix stripped from the final segment (Rails ".:format").
		"/v2/devices/{id}.json": "/v2/devices/{}",
		"/v2/bookmarks.json":    "/v2/bookmarks",
		"/v2/report.xml":        "/v2/report",
		// A version-like or genuinely dotted segment is not a format suffix.
		"/api/v2.5/items": "/api/v2.5/items",
	}
	for in, want := range cases {
		if got := normalizePath(in); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeLabel(t *testing.T) {
	for _, in := range []string{"app-web", "app_web", "AppWeb", "APP-WEB"} {
		if got := normalizeLabel(in); got != "appweb" {
			t.Errorf("normalizeLabel(%q) = %q, want appweb", in, got)
		}
	}
}

func TestIsGenericPath(t *testing.T) {
	generic := []string{"/health", "/status", "/metrics", "/"}
	for _, p := range generic {
		if !isGenericPath(normalizePath(p)) {
			t.Errorf("isGenericPath(%q) = false, want true", p)
		}
	}
	specific := []string{"/api/items", "/items/{id}", "/a/b"}
	for _, p := range specific {
		if isGenericPath(normalizePath(p)) {
			t.Errorf("isGenericPath(%q) = true, want false", p)
		}
	}
}

// --- (A) HTTP linking ---

func TestComputeLinks_HTTPMatch(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", nil),
		serverRoute("svc-beta", "/api/items/{itemId}", "GET"),
		serverRoute("svc-beta", "/api/items/{itemId}", "POST"), // method mismatch — ignored
	}
	out := ComputeLinks(in)

	if got, want := serviceNodes(out), []string{"svc-alpha", "svc-beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("service nodes = %v, want %v", got, want)
	}
	if !hasServiceEdge(out, "svc-alpha", "svc-beta") {
		t.Fatalf("svc-alpha service node missing depends_on svc-beta; got %+v", out)
	}
	e := findEdge(out, "svc-alpha", "svc-beta")
	if e == nil {
		t.Fatalf("missing svc-alpha -> svc-beta edge; got %+v", out)
	}
	if e.Props["type"] != "cross_repo" || e.Props["synthetic"] != SyntheticMarker {
		t.Errorf("edge props = %v", e.Props)
	}
	if via, _ := e.Props["via"].([]string); !reflect.DeepEqual(via, []string{"http"}) {
		t.Errorf("via = %v, want [http]", e.Props["via"])
	}
	if c, _ := e.Props["endpoint_count"].(int); c != 1 {
		t.Errorf("endpoint_count = %v, want 1", e.Props["endpoint_count"])
	}
}

// TestComputeLinks_ExternalClientBucketed verifies that a client route tagged
// external is counted in the external bucket, not unresolved, and produces no
// cross-repo edge — while internal calls still resolve/unresolve as before.
func TestComputeLinks_ExternalClientBucketed(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", nil),                                   // resolves to svc-beta
		clientRoute("svc-alpha", "/rest/api/2/issue", "POST", map[string]any{"external": true}),    // external third party
		clientRoute("svc-alpha", "/api/unknown/{id}", "GET", nil),                                  // internal, no server -> unresolved
		serverRoute("svc-beta", "/api/items/{itemId}", "GET"),
	}
	out := ComputeLinks(in)

	ec := edgeCoverageOf(out, "svc-alpha")
	if ec == nil {
		t.Fatalf("no edge_coverage on svc-alpha; got %+v", out)
	}
	if ec["detected"] != 3 || ec["resolved"] != 1 || ec["external"] != 1 || ec["unresolved"] != 1 {
		t.Errorf("coverage = detected:%v resolved:%v external:%v unresolved:%v; want 3/1/1/1",
			ec["detected"], ec["resolved"], ec["external"], ec["unresolved"])
	}
	// The external call must not create a cross-repo edge.
	if hasServiceEdge(out, "svc-alpha", "svc-beta") == false {
		t.Errorf("expected the internal /api/items edge to svc-beta")
	}
}

// TestUnmatchedClientRouteKeys verifies the per-call unresolved verdict mirrors the
// linker: resolved calls are absent, and each unresolved call carries its reason,
// while external calls are omitted.
func TestUnmatchedClientRouteKeys(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", nil),                                 // resolves
		clientRoute("svc-alpha", "/api/unknown/{id}", "GET", nil),                                // no server -> no_match
		clientRoute("svc-alpha", "/health", "GET", nil),                                          // 1 segment -> generic_path
		clientRoute("svc-alpha", "/api/things/{id}", "", nil),                                     // no verb -> no_method
		clientRoute("svc-alpha", "/rest/api/2/issue", "POST", map[string]any{"external": true}),  // external -> omitted
		serverRoute("svc-beta", "/api/items/{itemId}", "GET"),
	}
	got := UnmatchedClientRouteKeys(in)

	want := map[string]string{
		RouteIdentity(clientRoute("svc-alpha", "/api/unknown/{id}", "GET", nil)): "no_match",
		RouteIdentity(clientRoute("svc-alpha", "/health", "GET", nil)):           "generic_path",
		RouteIdentity(clientRoute("svc-alpha", "/api/things/{id}", "", nil)):     "no_method",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d unmatched, want %d: %+v", len(got), len(want), got)
	}
	for id, reason := range want {
		if got[id] != reason {
			t.Errorf("identity %q: got reason %q, want %q", id, got[id], reason)
		}
	}
	// The resolved and the external call must not appear.
	if _, bad := got[RouteIdentity(clientRoute("svc-alpha", "/api/items/{id}", "GET", nil))]; bad {
		t.Errorf("resolved call should not be flagged unmatched")
	}
}

// edgeCoverageOf returns the http_client edge_coverage map for a service node.
func edgeCoverageOf(out []facts.Fact, repo string) map[string]any {
	for _, f := range out {
		if f.Kind != facts.KindService || f.Name != repo {
			continue
		}
		list, _ := f.Props["edge_coverage"].([]map[string]any)
		for _, ec := range list {
			if ec["edge_type"] == "http_client" {
				return ec
			}
		}
	}
	return nil
}

func TestComputeLinks_HTTPGatewayPath(t *testing.T) {
	server := serverRoute("svc-beta", "/items/{id}", "GET")
	server.Props["gateway_path"] = "/api/catalogue/items/{id}"
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/catalogue/items/{id}", "GET", nil),
		server,
	}
	if findEdge(ComputeLinks(in), "svc-alpha", "svc-beta") == nil {
		t.Errorf("expected gateway-path match to produce an edge")
	}
}

// TestComputeLinks_HTTPClientPrefixMatch covers the BFF/gateway pattern: the client
// calls a path carrying an extra "/api/settings" prefix that the server (a different
// repo) serves un-prefixed. Client-suffix matching must still resolve the edge.
func TestComputeLinks_HTTPClientPrefixMatch(t *testing.T) {
	in := []facts.Fact{
		clientRoute("golf-ui", "/api/settings/tickets/{id}/resolve", "POST", nil),
		serverRoute("golf", "/tickets/{id:[0-9]+}/resolve", "POST"),
	}
	out := ComputeLinks(in)
	if !hasServiceEdge(out, "golf-ui", "golf") {
		t.Fatalf("client prefix path did not resolve to server; got %+v", out)
	}
	// Suffix (not full-path) match → probable, and the call counts as resolved.
	if _, r, u, ok := httpCoverageOf(out, "golf-ui"); !ok || r != 1 || u != 0 {
		t.Errorf("golf-ui coverage = resolved %d unresolved %d (ok=%v); want 1/0", r, u, ok)
	}
}

// TestComputeLinks_HTTPNextjsDynamicSegment checks that a Next.js-style server path
// with a [id] dynamic segment normalizes and matches a client {} placeholder.
func TestComputeLinks_HTTPNextjsDynamicSegment(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", nil),
		serverRoute("svc-beta", "/api/items/[id]", "GET"),
	}
	if !hasServiceEdge(ComputeLinks(in), "svc-alpha", "svc-beta") {
		t.Errorf("Next.js [id] server segment did not match client {} placeholder")
	}
}

// TestComputeLinks_HTTPFormatSuffixMatch checks that a client calling a
// ".json"-suffixed path (Retrofit/redux-tools/URLSession idiom) resolves to the
// backend route served without the format suffix.
func TestComputeLinks_HTTPFormatSuffixMatch(t *testing.T) {
	in := []facts.Fact{
		clientRoute("android", "v2/devices/{id}.json", "DELETE", nil),
		serverRoute("golf", "/devices/:id", "DELETE"),
	}
	if !hasServiceEdge(ComputeLinks(in), "android", "golf") {
		t.Errorf("client .json path did not match server route without the suffix")
	}
}

func TestComputeLinks_HTTPGenericPathSkipped(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/health", "GET", nil),
		serverRoute("svc-beta", "/health", "GET"),
	}
	if edges := crossRepoEdges(ComputeLinks(in)); len(edges) != 0 {
		t.Errorf("generic path produced edges: %+v", edges)
	}
}

func TestComputeLinks_HTTPSelfLinkSkipped(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", nil),
		serverRoute("svc-alpha", "/api/items/{id}", "GET"),
	}
	if out := ComputeLinks(in); len(out) != 0 {
		t.Errorf("self-link produced links: %+v", out)
	}
}

func TestComputeLinks_HTTPAmbiguousResolvedByHint(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", map[string]any{"api": "svc-beta"}),
		serverRoute("svc-beta", "/api/items/{id}", "GET"),
		serverRoute("svc-other", "/api/items/{id}", "GET"),
	}
	out := ComputeLinks(in)
	if findEdge(out, "svc-alpha", "svc-beta") == nil {
		t.Errorf("hint did not resolve to svc-beta: %+v", out)
	}
	if findEdge(out, "svc-alpha", "svc-other") != nil {
		t.Errorf("unexpected edge to svc-other")
	}
}

func TestComputeLinks_HTTPAmbiguousNoHintSkipped(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", nil),
		serverRoute("svc-beta", "/api/items/{id}", "GET"),
		serverRoute("svc-other", "/api/items/{id}", "GET"),
	}
	for _, f := range ComputeLinks(in) {
		if f.Kind == facts.KindDependency {
			t.Errorf("ambiguous match without hint produced edge: %+v", f)
		}
	}
}

// TestComputeLinks_CoverageBlindSpot checks that a service whose only outbound
// HTTP-client call site cannot be resolved to any loaded server is recorded as a
// coverage gap (detected>0, resolved=0) rather than a true isolate — while a
// service whose call site does resolve is fully covered.
func TestComputeLinks_CoverageBlindSpot(t *testing.T) {
	in := []facts.Fact{
		// svc-alpha calls a path no loaded repo serves -> unresolved blind spot.
		clientRoute("svc-alpha", "/api/orders/{id}", "GET", nil),
		// svc-gamma calls a path svc-beta serves -> resolved.
		clientRoute("svc-gamma", "/api/items/{id}", "GET", nil),
		serverRoute("svc-beta", "/api/items/{id}", "GET"),
	}
	out := ComputeLinks(in)

	// svc-alpha: detected but unresolved, and no outbound edge formed.
	if hasServiceEdge(out, "svc-alpha", "svc-beta") {
		t.Errorf("svc-alpha unexpectedly linked to svc-beta")
	}
	d, r, u, ok := httpCoverageOf(out, "svc-alpha")
	if !ok {
		t.Fatalf("svc-alpha has no edge_coverage; want a coverage gap")
	}
	if d != 1 || r != 0 || u != 1 {
		t.Errorf("svc-alpha coverage = detected %d resolved %d unresolved %d; want 1/0/1", d, r, u)
	}

	// svc-gamma: detected and fully resolved.
	d, r, u, ok = httpCoverageOf(out, "svc-gamma")
	if !ok {
		t.Fatalf("svc-gamma has no edge_coverage")
	}
	if d != 1 || r != 1 || u != 0 {
		t.Errorf("svc-gamma coverage = detected %d resolved %d unresolved %d; want 1/1/0", d, r, u)
	}

	// svc-beta serves but makes no client calls -> no coverage entry (true leaf).
	if _, _, _, ok := httpCoverageOf(out, "svc-beta"); ok {
		t.Errorf("svc-beta unexpectedly has edge_coverage; it makes no client calls")
	}
}

// --- (B) import linking ---

func TestComputeLinks_ImportMatch(t *testing.T) {
	in := []facts.Fact{
		importDep("app-web-app", "@app-web/lib-api/api-client-util"),
		facts.Fact{Kind: facts.KindModule, Name: "lib-api", Repo: "app-web"},
	}
	out := ComputeLinks(in)
	e := findEdge(out, "app-web-app", "app-web")
	if e == nil {
		t.Fatalf("missing import edge; got %+v", out)
	}
	if via, _ := e.Props["via"].([]string); !reflect.DeepEqual(via, []string{"import"}) {
		t.Errorf("via = %v, want [import]", e.Props["via"])
	}
	if c, _ := e.Props["import_count"].(int); c != 1 {
		t.Errorf("import_count = %v, want 1", e.Props["import_count"])
	}
}

func TestComputeLinks_ImportRubyStyle(t *testing.T) {
	in := []facts.Fact{
		importDep("svc-alpha", "lib-core/money/converter"),
		facts.Fact{Kind: facts.KindModule, Name: "money", Repo: "lib-core"},
	}
	if findEdge(ComputeLinks(in), "svc-alpha", "lib-core") == nil {
		t.Errorf("expected svc-alpha -> lib-core import edge")
	}
}

func TestComputeLinks_ImportRelativeAndSelfIgnored(t *testing.T) {
	in := []facts.Fact{
		importDep("svc-alpha", "./local/thing"),   // relative — skip
		importDep("svc-alpha", "svc-alpha/inner"), // self — skip
		facts.Fact{Kind: facts.KindModule, Name: "x", Repo: "lib-core"},
	}
	if edges := crossRepoEdges(ComputeLinks(in)); len(edges) != 0 {
		t.Errorf("relative/self imports produced edges: %+v", edges)
	}
}

// TestComputeLinks_ImportIntraRepoNamespaceSkipped guards the reverse-DNS false
// positive: a consumer's own file whose path embeds another repo's short label as
// an interior namespace segment (e.g. "de/backend/app/..." in an app whose top-level
// source dir is "app", vs a backend repo "backend") must NOT fabricate an import
// edge, while a genuine deep cross-repo path still links.
func TestComputeLinks_ImportIntraRepoNamespaceSkipped(t *testing.T) {
	in := []facts.Fact{
		// consumer "mobile" declares a top-level "app" source dir and imports its own
		// file whose path contains the interior segment "backend".
		module("mobile", "app/src/main/java/de/backend/app/ui"),
		importDep("mobile", "app/src/main/java/de/backend/app/ui/Screen"),
		// a real backend repo happens to be labeled "backend".
		serverRoute("backend", "/api/items/{id}", "GET"),
		// a genuine deep cross-repo import still resolves (leading seg not a mobile dir).
		importDep("mobile", "github.com/x/go-auth/adapters"),
		module("go-auth", "adapters"),
	}
	out := ComputeLinks(in)
	if findEdge(out, "mobile", "backend") != nil {
		t.Errorf("intra-repo namespace path must not link to a same-named repo; got edge to backend")
	}
	if findEdge(out, "mobile", "go-auth") == nil {
		t.Errorf("a genuine deep cross-repo import should still resolve to go-auth")
	}
}

// TestComputeLinks_ImportSelfNamedTargetSkipped covers an app whose own module is
// named the same short token as a backend repo (e.g. a mobile app target "acme"
// alongside a backend repo also labeled "acme"): importing it is intra-repo, not a
// call to the backend.
func TestComputeLinks_ImportSelfNamedTargetSkipped(t *testing.T) {
	in := []facts.Fact{
		module("app-ios", "acme"), // the app's own target module
		importDep("app-ios", "acme"),
		serverRoute("acme", "/api/items/{id}", "GET"),
	}
	if findEdge(ComputeLinks(in), "app-ios", "acme") != nil {
		t.Errorf("importing the app's own same-named module must not link to the backend repo")
	}
}

// --- (A+B) merge + determinism ---

func TestComputeLinks_MergedViaAndDeterministic(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", nil),
		serverRoute("svc-beta", "/api/items/{id}", "GET"),
		importDep("svc-alpha", "svc-beta/client"),
		facts.Fact{Kind: facts.KindModule, Name: "client", Repo: "svc-beta"},
	}
	out1 := ComputeLinks(in)
	e := findEdge(out1, "svc-alpha", "svc-beta")
	if e == nil {
		t.Fatalf("missing merged edge: %+v", out1)
	}
	if via, _ := e.Props["via"].([]string); !reflect.DeepEqual(via, []string{"http", "import"}) {
		t.Errorf("via = %v, want [http import]", e.Props["via"])
	}

	// Deterministic: identical input yields identical output.
	out2 := ComputeLinks(in)
	if !reflect.DeepEqual(out1, out2) {
		t.Errorf("ComputeLinks not deterministic:\n%+v\n%+v", out1, out2)
	}
}

func TestComputeLinks_SingleRepoNoLinks(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", nil),
		serverRoute("svc-alpha", "/api/items/{id}", "GET"),
	}
	if out := ComputeLinks(in); out != nil {
		t.Errorf("single repo produced links: %+v", out)
	}
}

// --- (A) suffix-aware HTTP matching ---

func TestComputeLinks_HTTPSuffixMatch(t *testing.T) {
	// golf serves the full /api/settings path; consumers call it with varying
	// prefixes (Swift base-relative, Kotlin/TS with /api). All must link to golf.
	in := []facts.Fact{
		serverRoute("golf", "/api/settings/entitlements/definitions", "GET"),
		clientRoute("ios", "settings/entitlements/definitions", "GET", nil), // base-relative, no slash
		clientRoute("android", "/api/settings/entitlements/definitions", "GET", nil),
		clientRoute("golf-ui", "/settings/entitlements/definitions", "GET", nil), // leading slash, no /api
	}
	out := ComputeLinks(in)
	for _, consumer := range []string{"ios", "android", "golf-ui"} {
		if findEdge(out, consumer, "golf") == nil {
			t.Errorf("%s did not link to golf via suffix match; out=%+v", consumer, out)
		}
		if !hasServiceEdge(out, consumer, "golf") {
			t.Errorf("%s service node missing depends_on golf", consumer)
		}
	}
}

func TestComputeLinks_HTTPSuffixMinSegments(t *testing.T) {
	// A single trailing segment is below minSharedSegments → no link.
	in := []facts.Fact{
		serverRoute("golf", "/api/settings/definitions", "GET"),
		clientRoute("ios", "definitions", "GET", nil),
	}
	if edges := crossRepoEdges(ComputeLinks(in)); len(edges) != 0 {
		t.Errorf("single-segment suffix should not link: %+v", edges)
	}
}

func TestComputeLinks_HTTPSuffixMethodMismatch(t *testing.T) {
	in := []facts.Fact{
		serverRoute("golf", "/api/settings/feedback", "GET"),
		clientRoute("golf-ui", "settings/feedback", "POST", nil),
	}
	if edges := crossRepoEdges(ComputeLinks(in)); len(edges) != 0 {
		t.Errorf("method mismatch should not link: %+v", edges)
	}
}

// --- (A2) per-repo service nodes ---

func TestComputeLinks_PerRepoServiceNodes(t *testing.T) {
	// Five loaded repos but only one real edge (golf -> go-auth import). Every
	// repo must still get an addressable service node; edgeless ones carry no
	// depends_on relations.
	in := []facts.Fact{
		importDep("golf", "github.com/x/go-auth/adapters"),
		facts.Fact{Kind: facts.KindModule, Name: "adapters", Repo: "go-auth"},
		facts.Fact{Kind: facts.KindModule, Name: "src/ui", Repo: "golf-ui"},
		facts.Fact{Kind: facts.KindModule, Name: "app", Repo: "android"},
		facts.Fact{Kind: facts.KindModule, Name: "App", Repo: "ios"},
	}
	out := ComputeLinks(in)

	got := serviceNodes(out)
	want := []string{"android", "go-auth", "golf", "golf-ui", "ios"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("service nodes = %v, want %v", got, want)
	}
	if findEdge(out, "golf", "go-auth") == nil {
		t.Errorf("expected the golf -> go-auth import edge to remain")
	}
	// An edgeless repo's service node has no depends_on relations.
	for _, f := range out {
		if f.Kind == facts.KindService && f.Name == "golf-ui" && len(f.Relations) != 0 {
			t.Errorf("isolated repo golf-ui should have no relations, got %+v", f.Relations)
		}
	}
}

// --- (C) shared symbol surface ---

func module(repo, name string) facts.Fact {
	return facts.Fact{Kind: facts.KindModule, Name: name, Repo: repo}
}

func typeSym(repo, name, kind string) facts.Fact {
	return facts.Fact{
		Kind:  facts.KindSymbol,
		Name:  name,
		Repo:  repo,
		Props: map[string]any{"symbol_kind": kind},
	}
}

func TestComputeLinks_SharedSymbolsMatch(t *testing.T) {
	// getdp and gmsh both declare the vendored onelab/GmshSocket types, under
	// different directory prefixes (src/common vs Common). Enough distinctive
	// shared types must link them, bidirectionally and via shared_symbols.
	in := []facts.Fact{
		module("getdp", "src/common"),
		typeSym("getdp", "src/common.GmshClient", facts.SymbolClass),
		typeSym("getdp", "src/common.GmshServer", facts.SymbolClass),
		typeSym("getdp", "src/common.onelab::remoteNetworkClient", facts.SymbolClass),
		module("gmsh", "Common"),
		typeSym("gmsh", "Common.GmshClient", facts.SymbolClass),
		typeSym("gmsh", "Common.GmshServer", facts.SymbolClass),
		typeSym("gmsh", "Common.onelab::remoteNetworkClient", facts.SymbolClass),
	}
	out := ComputeLinks(in)

	for _, pair := range [][2]string{{"getdp", "gmsh"}, {"gmsh", "getdp"}} {
		e := findEdge(out, pair[0], pair[1])
		if e == nil {
			t.Fatalf("missing shared-symbol edge %s -> %s; out=%+v", pair[0], pair[1], out)
		}
		if via, _ := e.Props["via"].([]string); !reflect.DeepEqual(via, []string{"shared_symbols"}) {
			t.Errorf("via = %v, want [shared_symbols]", e.Props["via"])
		}
		if c, _ := e.Props["symbol_count"].(int); c != 3 {
			t.Errorf("symbol_count = %v, want 3", e.Props["symbol_count"])
		}
		if !hasServiceEdge(out, pair[0], pair[1]) {
			t.Errorf("%s service node missing depends_on %s", pair[0], pair[1])
		}
	}
}

func TestComputeLinks_SharedSymbolsBelowThreshold(t *testing.T) {
	// Only one distinctive shared type (below minSharedSymbols) → no link.
	in := []facts.Fact{
		module("alpha", "core"),
		typeSym("alpha", "core.WidgetRegistry", facts.SymbolClass),
		module("beta", "lib"),
		typeSym("beta", "lib.WidgetRegistry", facts.SymbolClass),
	}
	if edges := crossRepoEdges(ComputeLinks(in)); len(edges) != 0 {
		t.Errorf("single shared type should not link: %+v", edges)
	}
}

func TestComputeLinks_SharedSymbolsGenericNamesIgnored(t *testing.T) {
	// Common generic/short unqualified type names are not distinctive enough to
	// link two otherwise-unrelated repos, even at count >= threshold.
	in := []facts.Fact{
		module("alpha", "core"),
		typeSym("alpha", "core.Config", facts.SymbolClass),
		typeSym("alpha", "core.Error", facts.SymbolStruct),
		typeSym("alpha", "core.Node", facts.SymbolClass),
		typeSym("alpha", "core.Item", facts.SymbolClass),
		module("beta", "lib"),
		typeSym("beta", "lib.Config", facts.SymbolClass),
		typeSym("beta", "lib.Error", facts.SymbolStruct),
		typeSym("beta", "lib.Node", facts.SymbolClass),
		typeSym("beta", "lib.Item", facts.SymbolClass),
	}
	if edges := crossRepoEdges(ComputeLinks(in)); len(edges) != 0 {
		t.Errorf("generic shared names should not link: %+v", edges)
	}
}

func TestComputeLinks_SharedSymbolsNonTypesIgnored(t *testing.T) {
	// Functions/methods/variables are not the contract surface; sharing them
	// (even many) must not link repos.
	in := []facts.Fact{
		module("alpha", "core"),
		typeSym("alpha", "core.processRequest", facts.SymbolFunc),
		typeSym("alpha", "core.parseHeader", facts.SymbolFunc),
		typeSym("alpha", "core.computeChecksum", facts.SymbolFunc),
		module("beta", "lib"),
		typeSym("beta", "lib.processRequest", facts.SymbolFunc),
		typeSym("beta", "lib.parseHeader", facts.SymbolFunc),
		typeSym("beta", "lib.computeChecksum", facts.SymbolFunc),
	}
	if edges := crossRepoEdges(ComputeLinks(in)); len(edges) != 0 {
		t.Errorf("shared non-type symbols should not link: %+v", edges)
	}
}

func typeSymLang(repo, name, kind, lang string) facts.Fact {
	f := typeSym(repo, name, kind)
	f.Props["language"] = lang
	return f
}

// TestComputeLinks_SharedSymbolsCrossLanguageUnqualifiedSkipped covers the parallel-
// app false positive: two apps in different languages declaring the same plain
// domain type names (e.g. Kotlin and Swift both defining LoginViewModel/FeedItem/
// AccountViewModel) are modeling the same product, not sharing code — no link.
func TestComputeLinks_SharedSymbolsCrossLanguageUnqualifiedSkipped(t *testing.T) {
	in := []facts.Fact{
		module("android", "app"),
		typeSymLang("android", "app.LoginViewModel", facts.SymbolClass, "kotlin"),
		typeSymLang("android", "app.FeedItem", facts.SymbolClass, "kotlin"),
		typeSymLang("android", "app.AccountViewModel", facts.SymbolClass, "kotlin"),
		module("ios", "Sources"),
		typeSymLang("ios", "Sources.LoginViewModel", facts.SymbolClass, "swift"),
		typeSymLang("ios", "Sources.FeedItem", facts.SymbolClass, "swift"),
		typeSymLang("ios", "Sources.AccountViewModel", facts.SymbolClass, "swift"),
	}
	if edges := crossRepoEdges(ComputeLinks(in)); len(edges) != 0 {
		t.Errorf("unqualified domain types shared across languages must not link: %+v", edges)
	}
}

// TestComputeLinks_SharedSymbolsSameLanguageUnqualifiedLinks confirms the fix does
// not over-reach: enough unqualified types shared between SAME-language repos still
// link (genuine copied source).
func TestComputeLinks_SharedSymbolsSameLanguageUnqualifiedLinks(t *testing.T) {
	in := []facts.Fact{
		module("svc-a", "core"),
		typeSymLang("svc-a", "core.WidgetRegistry", facts.SymbolClass, "go"),
		typeSymLang("svc-a", "core.PaymentLedger", facts.SymbolClass, "go"),
		typeSymLang("svc-a", "core.RetryPolicy", facts.SymbolClass, "go"),
		module("svc-b", "lib"),
		typeSymLang("svc-b", "lib.WidgetRegistry", facts.SymbolClass, "go"),
		typeSymLang("svc-b", "lib.PaymentLedger", facts.SymbolClass, "go"),
		typeSymLang("svc-b", "lib.RetryPolicy", facts.SymbolClass, "go"),
	}
	if findEdge(ComputeLinks(in), "svc-a", "svc-b") == nil {
		t.Errorf("same-language repos sharing distinctive types should still link")
	}
}

// TestComputeLinks_SharedSymbolsQualifiedCrossLanguageLinks confirms a namespace-
// qualified identity (the vendored/shared-source signal) links regardless of the
// per-repo dominant language.
func TestComputeLinks_SharedSymbolsQualifiedCrossLanguageLinks(t *testing.T) {
	in := []facts.Fact{
		module("repo-a", "src"),
		typeSymLang("repo-a", "src.onelab::clientA", facts.SymbolClass, "cpp"),
		typeSymLang("repo-a", "src.onelab::clientB", facts.SymbolClass, "cpp"),
		typeSymLang("repo-a", "src.onelab::clientC", facts.SymbolClass, "cpp"),
		module("repo-b", "vendor"),
		typeSymLang("repo-b", "vendor.onelab::clientA", facts.SymbolClass, "c"),
		typeSymLang("repo-b", "vendor.onelab::clientB", facts.SymbolClass, "c"),
		typeSymLang("repo-b", "vendor.onelab::clientC", facts.SymbolClass, "c"),
	}
	if findEdge(ComputeLinks(in), "repo-a", "repo-b") == nil {
		t.Errorf("qualified shared identities should link even across languages")
	}
}

// --- via: http-client tag and confidence ---

func TestComputeLinks_HTTPClientViaTag(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/list", "GET", map[string]any{"source": "go-http-client"}),
		serverRoute("svc-beta", "/api/items/list", "GET"),
	}
	e := findEdge(ComputeLinks(in), "svc-alpha", "svc-beta")
	if e == nil {
		t.Fatal("missing svc-alpha -> svc-beta edge")
	}
	if via, _ := e.Props["via"].([]string); !reflect.DeepEqual(via, []string{"http-client"}) {
		t.Errorf("via = %v, want [http-client]", e.Props["via"])
	}
}

func TestComputeLinks_OpenAPIViaStaysHTTP(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/list", "GET", map[string]any{"source": "openapi"}),
		serverRoute("svc-beta", "/api/items/list", "GET"),
	}
	e := findEdge(ComputeLinks(in), "svc-alpha", "svc-beta")
	if e == nil {
		t.Fatal("missing edge")
	}
	if via, _ := e.Props["via"].([]string); !reflect.DeepEqual(via, []string{"http"}) {
		t.Errorf("via = %v, want [http]", e.Props["via"])
	}
}

func TestComputeLinks_ConfidenceVerified(t *testing.T) {
	// Sole provider, client calls the full server path, no inferred {}.
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/list", "GET", nil),
		serverRoute("svc-beta", "/api/items/list", "GET"),
	}
	e := findEdge(ComputeLinks(in), "svc-alpha", "svc-beta")
	if e == nil || e.Props["confidence"] != "verified" {
		t.Errorf("confidence = %v, want verified", e.Props["confidence"])
	}
}

func TestComputeLinks_ConfidenceProbableSuffixOnly(t *testing.T) {
	// Client calls a base-relative subpath (suffix), not the full server path.
	in := []facts.Fact{
		clientRoute("svc-alpha", "settings/feedback", "POST", nil),
		serverRoute("svc-beta", "/api/settings/feedback", "POST"),
	}
	e := findEdge(ComputeLinks(in), "svc-alpha", "svc-beta")
	if e == nil || e.Props["confidence"] != "probable" {
		t.Errorf("confidence = %v, want probable", e.Props["confidence"])
	}
}

func TestComputeLinks_ConfidenceProbableInferred(t *testing.T) {
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/{id}", "GET", nil),
		serverRoute("svc-beta", "/api/items/{id}", "GET"),
	}
	e := findEdge(ComputeLinks(in), "svc-alpha", "svc-beta")
	if e == nil || e.Props["confidence"] != "probable" {
		t.Errorf("confidence = %v, want probable (inferred {})", e.Props["confidence"])
	}
}

func TestComputeLinks_ConfidenceProbableHintDisambiguated(t *testing.T) {
	// Two providers serve the path; resolved only via target_hint → probable.
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/list", "GET", map[string]any{"target_hint": "svcbeta"}),
		serverRoute("svc-beta", "/api/items/list", "GET"),
		serverRoute("svc-other", "/api/items/list", "GET"),
	}
	e := findEdge(ComputeLinks(in), "svc-alpha", "svc-beta")
	if e == nil {
		t.Fatal("missing svc-alpha -> svc-beta edge")
	}
	if e.Props["confidence"] != "probable" {
		t.Errorf("confidence = %v, want probable", e.Props["confidence"])
	}
	if findEdge(ComputeLinks(in), "svc-alpha", "svc-other") != nil {
		t.Error("should not link to svc-other")
	}
}

func TestComputeLinks_ConfidenceMixedIsVerified(t *testing.T) {
	// One verified endpoint + one probable endpoint between the same pair → verified.
	in := []facts.Fact{
		clientRoute("svc-alpha", "/api/items/list", "GET", nil),    // verified
		clientRoute("svc-alpha", "settings/feedback", "POST", nil), // probable (suffix)
		serverRoute("svc-beta", "/api/items/list", "GET"),
		serverRoute("svc-beta", "/api/settings/feedback", "POST"),
	}
	e := findEdge(ComputeLinks(in), "svc-alpha", "svc-beta")
	if e == nil || e.Props["confidence"] != "verified" {
		t.Errorf("confidence = %v, want verified", e.Props["confidence"])
	}
}

func TestComputeLinks_TargetHintResolvesProvider(t *testing.T) {
	// target_hint "svccheckout" (from SvcCheckoutClient) resolves svc-checkout.
	in := []facts.Fact{
		clientRoute("core", "/api/purchase/build", "POST", map[string]any{"target_hint": "svccheckout"}),
		serverRoute("svc-checkout", "/api/purchase/build", "POST"),
		serverRoute("svc-other", "/api/purchase/build", "POST"),
	}
	out := ComputeLinks(in)
	if findEdge(out, "core", "svc-checkout") == nil {
		t.Error("target_hint should resolve provider svc-checkout")
	}
	if findEdge(out, "core", "svc-other") != nil {
		t.Error("should not link to svc-other")
	}
}

// --- server-side inverse: routes unused by loaded clients ---

func hasRouteKey(keys map[string]bool, repo, method, path string) bool {
	return keys[routeIdentityKey(repo, method, path)]
}

func TestUnmatchedServerRouteKeys_BasicSetDifference(t *testing.T) {
	in := []facts.Fact{
		// golf-ui calls one of golf's two routes.
		clientRoute("golf-ui", "/api/items/{id}", "GET", nil),
		serverRoute("golf", "/api/items/{id}", "GET"),     // called → matched
		serverRoute("golf", "/api/secret/cleanup", "POST"), // no caller → unmatched
	}
	keys := UnmatchedServerRouteKeys(in)

	if hasRouteKey(keys, "golf", "GET", "/api/items/{id}") {
		t.Errorf("route called by a client must not be flagged unused; keys=%v", keys)
	}
	if !hasRouteKey(keys, "golf", "POST", "/api/secret/cleanup") {
		t.Errorf("route with no caller must be flagged unused; keys=%v", keys)
	}
	if len(keys) != 1 {
		t.Errorf("expected exactly 1 unmatched route, got %d: %v", len(keys), keys)
	}
}

// The false-positive guard: a client calling with a different leading prefix than
// the server serves (golf-ui "/api/settings/x" vs golf "settings/x", and the
// reverse) must still count as matched — exactly the normalization linkHTTP does.
func TestUnmatchedServerRouteKeys_PrefixDifferenceIsMatched(t *testing.T) {
	in := []facts.Fact{
		clientRoute("golf-ui", "/api/settings/feedback", "POST", nil),
		clientRoute("ios", "settings/ai-coach/insight", "POST", nil), // base-relative
		serverRoute("golf", "/api/settings/feedback", "POST"),
		serverRoute("golf", "/api/settings/ai-coach/insight", "POST"),
	}
	keys := UnmatchedServerRouteKeys(in)
	if len(keys) != 0 {
		t.Errorf("prefix/base-relative client calls should match their server routes; "+
			"none should be flagged unused, got %v", keys)
	}
}

// A pure-consumer repo (a frontend serving its own page routes while only calling
// another repo's API) is not an HTTP provider, so its own uncalled server routes
// must NOT be flagged — only the provider's (golf's) uncalled routes are.
func TestUnmatchedServerRouteKeys_ConsumerOwnRoutesNotFlagged(t *testing.T) {
	in := []facts.Fact{
		// golf-ui calls golf's API (making golf a provider) and serves its own page.
		clientRoute("golf-ui", "/api/items/{id}", "GET", nil),
		serverRoute("golf-ui", "/dashboard/settings", "GET"), // golf-ui's own page, no caller
		serverRoute("golf", "/api/items/{id}", "GET"),         // called by golf-ui
		serverRoute("golf", "/api/secret/cleanup", "POST"),    // no caller
	}
	keys := UnmatchedServerRouteKeys(in)
	if hasRouteKey(keys, "golf-ui", "GET", "/dashboard/settings") {
		t.Errorf("a pure-consumer repo's own routes must not be flagged; keys=%v", keys)
	}
	if !hasRouteKey(keys, "golf", "POST", "/api/secret/cleanup") {
		t.Errorf("the provider's uncalled route must still be flagged; keys=%v", keys)
	}
	if len(keys) != 1 {
		t.Errorf("expected exactly 1 unmatched route (golf only), got %d: %v", len(keys), keys)
	}
}

func TestUnmatchedServerRouteKeys_SingleRepoReturnsNil(t *testing.T) {
	in := []facts.Fact{
		serverRoute("golf", "/api/items/{id}", "GET"),
		serverRoute("golf", "/api/secret/cleanup", "POST"),
	}
	if keys := UnmatchedServerRouteKeys(in); keys != nil {
		t.Errorf("single-repo snapshot has no clients to be unused by; want nil, got %v", keys)
	}
}

func TestRouteIdentityMatchesKeyHelper(t *testing.T) {
	f := serverRoute("golf", "/api/items/{id}", "get") // lowercase method
	if got, want := RouteIdentity(f), routeIdentityKey("golf", "GET", "/api/items/{id}"); got != want {
		t.Errorf("RouteIdentity normalized mismatch: got %q want %q", got, want)
	}
}
