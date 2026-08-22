package providers

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

const validRuntimeRouteFact = `{"kind":"route","name":"runtime-route: GET /health","file":".enola-runtime/boot.json","props":{"resolution_level":"runtime-observed","observed_via":"rails-boot","method":"GET","path":"/health"}}`

func TestParseFactLine_RuntimeObservedIsInTheVocabulary(t *testing.T) {
	f, err := parseFactLine(validRuntimeRouteFact)
	if err != nil {
		t.Fatalf("a runtime-observed fact with an observation channel must validate, got %v", err)
	}
	if f.Props[PropResolutionLevel] != LevelRuntimeObserved || f.Props[PropObservedVia] != "rails-boot" {
		t.Errorf("props = %+v", f.Props)
	}
}

func TestParseFactLine_LevelOutsideTheVocabularyIsRejected(t *testing.T) {
	_, err := parseFactLine(`{"kind":"symbol","name":"x","props":{"resolution_level":"vibes"}}`)
	if err == nil || !strings.Contains(err.Error(), "not in the vocabulary") {
		t.Fatalf("err = %v, want a named vocabulary rejection", err)
	}
	for level := range allowedResolutionLevels {
		if _, err := parseFactLine(`{"kind":"symbol","name":"x","props":{"resolution_level":"` + level + `","observed_via":"rails-boot","declared_in":"sig/x.rbs"}}`); err != nil {
			t.Errorf("level %q must validate, got %v", level, err)
		}
	}
}

func TestParseFactLine_RuntimeObservedWithoutChannelIsRejected(t *testing.T) {
	_, err := parseFactLine(`{"kind":"route","name":"runtime-route: GET /x","props":{"resolution_level":"runtime-observed"}}`)
	if err == nil || !strings.Contains(err.Error(), PropObservedVia) {
		t.Fatalf("err = %v, want a rejection naming the missing observation channel", err)
	}
}

func routeStore(t *testing.T, ff ...facts.Fact) *facts.Store {
	t.Helper()
	store := facts.NewStore()
	store.Add(ff...)
	return store
}

func runtimeRouteFact(method, path, via string) facts.Fact {
	return facts.Fact{Kind: facts.KindRoute, Name: "runtime-route: " + method + " " + path,
		File: ".enola-runtime/boot.json",
		Props: map[string]any{PropResolutionLevel: LevelRuntimeObserved, PropObservedVia: via,
			"method": method, "path": path}}
}

func TestLinkRuntimeObservations_AnnotatesTheMatchingExtractedRoute(t *testing.T) {
	store := routeStore(t,
		facts.Fact{Kind: facts.KindRoute, Name: "/health", File: "config/routes.rb",
			Props: map[string]any{"method": "GET"}},
		facts.Fact{Kind: facts.KindRoute, Name: "/admin", File: "config/routes.rb",
			Props: map[string]any{"method": "GET"}},
		runtimeRouteFact("GET", "/health", "rails-boot"),
	)
	if n := LinkRuntimeObservations(store, 0); n != 1 {
		t.Fatalf("annotated = %d, want 1", n)
	}
	byName := map[string]facts.Fact{}
	for _, f := range store.FactsRef() {
		byName[f.Name] = f
	}
	got := byName["/health"]
	if got.Props[PropRuntimeObserved] != true || got.Props[PropObservedVia] != "rails-boot" {
		t.Errorf("matched route props = %+v, want runtime_observed true via rails-boot", got.Props)
	}
	unmatched := byName["/admin"]
	if _, claimed := unmatched.Props[PropRuntimeObserved]; claimed {
		t.Errorf("a route the booted table does not serve must stay unannotated: %+v", unmatched.Props)
	}
	observation := byName["runtime-route: GET /health"]
	if _, claimed := observation.Props[PropRuntimeObserved]; claimed {
		t.Errorf("the observation itself must never be annotated: %+v", observation.Props)
	}
}

func TestLinkRuntimeObservations_MethodMustMatchExactly(t *testing.T) {
	store := routeStore(t,
		facts.Fact{Kind: facts.KindRoute, Name: "/health", File: "config/routes.rb",
			Props: map[string]any{"method": "POST"}},
		runtimeRouteFact("GET", "/health", "rails-boot"),
	)
	if n := LinkRuntimeObservations(store, 0); n != 0 {
		t.Fatalf("annotated = %d, want 0: a differing verb is no match", n)
	}
}

func TestLinkRuntimeObservations_PathPropWinsOverName(t *testing.T) {
	store := routeStore(t,
		facts.Fact{Kind: facts.KindRoute, Name: "health.show", File: "config/routes.rb",
			Props: map[string]any{"method": "GET", "path": "/health"}},
		runtimeRouteFact("GET", "/health", "rails-boot"),
	)
	if n := LinkRuntimeObservations(store, 0); n != 1 {
		t.Fatalf("annotated = %d, want 1: the path prop is the route's identity when present", n)
	}
}

func TestLinkRuntimeObservations_ClientRoutesAreNeverAnnotated(t *testing.T) {
	store := routeStore(t,
		facts.Fact{Kind: facts.KindRoute, Name: "/health", File: "app/client.rb",
			Props: map[string]any{"method": "GET", "role": "client"}},
		runtimeRouteFact("GET", "/health", "rails-boot"),
	)
	if n := LinkRuntimeObservations(store, 0); n != 0 {
		t.Fatalf("annotated = %d, want 0: a client call site is not a served endpoint", n)
	}
}

func TestLinkRuntimeObservations_ChannelsMergeSortedAndIdempotently(t *testing.T) {
	store := routeStore(t,
		facts.Fact{Kind: facts.KindRoute, Name: "/health", File: "config/routes.rb",
			Props: map[string]any{"method": "GET"}},
		runtimeRouteFact("GET", "/health", "rails-boot"),
		facts.Fact{Kind: facts.KindRoute, Name: "runtime-route: GET /health (queries)",
			File: ".enola-runtime/queries.json",
			Props: map[string]any{PropResolutionLevel: LevelRuntimeObserved, PropObservedVia: "query-counter",
				"method": "GET", "path": "/health"}},
	)
	LinkRuntimeObservations(store, 0)
	LinkRuntimeObservations(store, 0)
	var got facts.Fact
	for _, f := range store.FactsRef() {
		if f.Name == "/health" {
			got = f
		}
	}
	if got.Props[PropObservedVia] != "query-counter rails-boot" {
		t.Errorf("observed_via = %q, want the sorted merged channel set", got.Props[PropObservedVia])
	}
}

func TestLinkRuntimeObservations_StaysInsideTheWindow(t *testing.T) {
	store := routeStore(t,
		facts.Fact{Kind: facts.KindRoute, Name: "/health", File: "config/routes.rb", Repo: "earlier",
			Props: map[string]any{"method": "GET"}},
	)
	windowStart := store.Count()
	store.Add(runtimeRouteFact("GET", "/health", "rails-boot"))
	if n := LinkRuntimeObservations(store, windowStart); n != 0 {
		t.Fatalf("annotated = %d, want 0: an earlier repo's route is outside this observation's window", n)
	}
	if _, claimed := store.FactsRef()[0].Props[PropRuntimeObserved]; claimed {
		t.Error("a fact before the window start was annotated")
	}
}

func runtimeScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "examples", "providers", "ruby", "runtime", "enola_runtime_provider.rb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("runtime provider missing: %v", err)
	}
	return path
}

func writeRuntimeFixture(t *testing.T, captures map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".enola-runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range captures {
		if err := os.WriteFile(filepath.Join(repo, ".enola-runtime", name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

const bootCapture = `{
  "schema": 1,
  "source": "enola runtime",
  "rails_version": "8.0.2",
  "app": "Fixture",
  "unreachable": [],
  "facts": [
    {"kind": "route", "name": "GET /health", "verb": "GET", "path": "/health", "endpoint_kind": "action", "controller": "health", "action": "show", "handler": "health#show"},
    {"kind": "route", "name": "GET /cookie_policy", "verb": "GET", "path": "/cookie_policy", "endpoint_kind": "redirect"},
    {"kind": "association", "name": "Order#items", "model": "Order", "association": "items", "macro": "has_many", "target": "Item", "through": null, "polymorphic": false},
    {"kind": "storage", "name": "orders", "model": "Order"}
  ]
}`

const queryCapture = `{
  "schema": 1,
  "source": "activesupport-notifications",
  "frames": 1,
  "total_queries": 276,
  "counts": [
    {"frame": "app/services/permissions/create_default_permissions.rb:add_full_permissions_to_role", "queries": 276}
  ]
}`

func TestRuntimeProvider_GoldenThroughTheSeam(t *testing.T) {
	requireRuby(t)
	repo := writeRuntimeFixture(t, map[string]string{"boot.json": bootCapture, "queries.json": queryCapture})
	ff, records := Run(context.Background(), []Provider{{
		Name:            "runtime",
		Command:         []string{"ruby", runtimeScript(t)},
		ExpectedVersion: "0.1.0",
	}}, repo, nil, nil)
	if len(records) != 1 || records[0].Skipped {
		t.Fatalf("census = %+v, want a clean run", records)
	}
	if records[0].Version != "0.1.0" || records[0].FactCount != 5 {
		t.Errorf("census = %+v, want version 0.1.0 and 5 facts", records[0])
	}

	byName := map[string]facts.Fact{}
	for _, f := range ff {
		if f.Props[PropResolutionLevel] != LevelRuntimeObserved {
			t.Errorf("fact %q level = %v, want %s", f.Name, f.Props[PropResolutionLevel], LevelRuntimeObserved)
		}
		if f.Props[PropProvider] != "runtime" || f.Props[PropProviderVersion] != "0.1.0" {
			t.Errorf("fact %q not stamped with provenance: %+v", f.Name, f.Props)
		}
		byName[f.Name] = f
	}

	handled := byName["runtime-route: GET /health -> health#show"]
	if handled.Kind != facts.KindRoute || handled.File != ".enola-runtime/boot.json" ||
		handled.Props[PropObservedVia] != "rails-boot" || handled.Props["method"] != "GET" ||
		handled.Props["path"] != "/health" || handled.Props["endpoint_kind"] != "action" ||
		handled.Props["handler"] != "health#show" {
		t.Errorf("handled route = %+v", handled)
	}
	redirect := byName["runtime-route: GET /cookie_policy"]
	if redirect.Kind != facts.KindRoute || redirect.Props["endpoint_kind"] != "redirect" {
		t.Errorf("redirect route = %+v", redirect)
	}
	association := byName["runtime-association: Order#items"]
	if association.Kind != facts.KindAssociation || association.Props["macro"] != "has_many" ||
		association.Props["target"] != "Item" {
		t.Errorf("association = %+v", association)
	}
	storage := byName["runtime-storage: Order -> orders"]
	if storage.Kind != facts.KindStorage || storage.Props["table"] != "orders" || storage.Props["model"] != "Order" {
		t.Errorf("storage = %+v", storage)
	}
	queries := byName["runtime-queries: app/services/permissions/create_default_permissions.rb:add_full_permissions_to_role"]
	if queries.Kind != facts.KindDependency ||
		queries.File != "app/services/permissions/create_default_permissions.rb" ||
		queries.Props[PropObservedVia] != "query-counter" ||
		queries.Props["frame_label"] != "add_full_permissions_to_role" ||
		queries.Props["queries"] != float64(276) {
		t.Errorf("query-count fact = %+v", queries)
	}
}

func TestRuntimeProvider_NoCapturesIsAnEmptyContribution(t *testing.T) {
	requireRuby(t)
	repo := t.TempDir()
	ff, records := Run(context.Background(), []Provider{{
		Name:    "runtime",
		Command: []string{"ruby", runtimeScript(t)},
	}}, repo, nil, nil)
	if len(records) != 1 || records[0].Skipped || records[0].FactCount != 0 {
		t.Fatalf("census = %+v, want a clean zero-fact run", records)
	}
	if len(ff) != 0 {
		t.Fatalf("facts = %+v, want none", ff)
	}
}

func TestRuntimeProvider_IncompleteBootRefusesTheWholeCapture(t *testing.T) {
	requireRuby(t)
	holed := strings.Replace(bootCapture, `"unreachable": []`,
		`"unreachable": [{"subject": "eager_load", "error": "KeyError", "message": "key not found"}]`, 1)
	repo := writeRuntimeFixture(t, map[string]string{"boot.json": holed})
	ff, records := Run(context.Background(), []Provider{{
		Name:    "runtime",
		Command: []string{"ruby", runtimeScript(t)},
	}}, repo, nil, nil)
	if len(ff) != 0 {
		t.Fatalf("facts = %+v, want none: a capture with holes must not become partial truth", ff)
	}
	if len(records) != 1 || !records[0].Skipped || !strings.Contains(records[0].Reason, "unreachable") {
		t.Fatalf("census = %+v, want a skip naming the unreachable subjects", records)
	}
}

func TestRuntimeProvider_UnrecognizedCaptureRefuses(t *testing.T) {
	requireRuby(t)
	repo := writeRuntimeFixture(t, map[string]string{"other.json": `{"source": "somebody-else", "facts": []}`})
	ff, records := Run(context.Background(), []Provider{{
		Name:    "runtime",
		Command: []string{"ruby", runtimeScript(t)},
	}}, repo, nil, nil)
	if len(ff) != 0 || len(records) != 1 || !records[0].Skipped ||
		!strings.Contains(records[0].Reason, "unrecognized capture source") {
		t.Fatalf("facts = %+v, census = %+v, want a named refusal", ff, records)
	}
}

func TestRuntimeProvider_OutputIsSortedAndDeterministic(t *testing.T) {
	requireRuby(t)
	repo := writeRuntimeFixture(t, map[string]string{"boot.json": bootCapture, "queries.json": queryCapture})
	script := runtimeScript(t)
	run := func() []byte {
		cmd := exec.Command("ruby", script, repo)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("provider run failed: %v (%s)", err, stderr.String())
		}
		return stdout.Bytes()
	}
	first, second := run(), run()
	if !bytes.Equal(first, second) {
		t.Fatalf("provider output differs across identical runs:\n%s\nvs\n%s", first, second)
	}
	lines := strings.Split(strings.TrimSpace(string(first)), "\n")
	if !sort.StringsAreSorted(lines) {
		t.Errorf("output lines are not sorted:\n%s", first)
	}
}
