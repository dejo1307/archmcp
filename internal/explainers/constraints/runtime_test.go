package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/providers"
)

func runtimeObservedRoute(method, path string) facts.Fact {
	return facts.Fact{Kind: facts.KindRoute, Name: "runtime-route: " + method + " " + path,
		File: ".enola-runtime/boot.json",
		Props: map[string]any{
			providers.PropResolutionLevel: providers.LevelRuntimeObserved,
			providers.PropObservedVia:     "rails-boot",
			"method":                      method,
			"path":                        path,
		}}
}

func TestExplain_ForbidFactVerdictCitesARuntimeObservedFact(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindIntent, Name: "component: retired-endpoints", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "component", "component": "retired-endpoints",
				"match": ".enola-runtime/**", "kind": "route",
				"name_pattern": "runtime-route: GET /legacy/export", "source": "wiki/p.md"}},
		facts.Fact{Kind: facts.KindIntent, Name: "rule: no-retired-endpoints", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "no-retired-endpoints",
				"forbid_fact": "retired-endpoints",
				"because":     "the endpoint was retired; a booted application must not serve it",
				"source":      "wiki/p.md"}},
		runtimeObservedRoute("GET", "/legacy/export"),
		runtimeObservedRoute("GET", "/health"),
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	want := "Constraint no-retired-endpoints violated: runtime-route: GET /legacy/export is measured in retired-endpoints"
	if got.Title != want {
		t.Errorf("title = %q, want %q", got.Title, want)
	}
	if got.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0: the rule is declared and the observation is measured", got.Confidence)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].File != ".enola-runtime/boot.json" ||
		got.Evidence[0].Symbol != "runtime-route: GET /legacy/export" {
		t.Errorf("evidence = %+v, want the runtime-observed fact and its capture file", got.Evidence)
	}
}

func TestExplain_RequireVerdictsOverTheRuntimeObservedAnnotation(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindIntent, Name: "component: public-api", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "component", "component": "public-api",
				"match": "config/routes.rb", "kind": "route", "source": "wiki/p.md"}},
		facts.Fact{Kind: facts.KindIntent, Name: "rule: public-api-is-served", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "public-api-is-served",
				"require": "public-api", "must_prop": providers.PropObservedVia, "must_value": "rails-boot",
				"because": "every public endpoint must exist in the booted route table",
				"source":  "wiki/p.md"}},
		facts.Fact{Kind: facts.KindRoute, Name: "/health", File: "config/routes.rb",
			Props: map[string]any{"method": "GET",
				providers.PropRuntimeObserved: true,
				providers.PropObservedVia:     "query-counter rails-boot"}},
		facts.Fact{Kind: facts.KindRoute, Name: "/ghost", File: "config/routes.rb",
			Props: map[string]any{"method": "GET"}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	if !strings.Contains(got.Title, "/ghost must have observed_via containing rails-boot") {
		t.Errorf("title = %q, want the unobserved route named", got.Title)
	}
	if strings.Contains(got.Title, "/health") {
		t.Errorf("title = %q: the runtime-observed route satisfies the rule and must stay silent", got.Title)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Symbol != "/ghost" {
		t.Errorf("evidence = %+v", got.Evidence)
	}
}
