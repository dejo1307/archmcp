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
