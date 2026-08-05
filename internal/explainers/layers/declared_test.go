package layers

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func layerIntent(repo, name string, order int, paths ...string) facts.Fact {
	return facts.Fact{Kind: facts.KindIntent, Repo: repo, Name: "layer " + name,
		Props: map[string]any{"intent_kind": "layer", "layer_name": name, "order": order, "paths": paths}}
}

func TestDeclaredLayers_WrongDirectionImportIsProofClass(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		layerIntent("svc", "handlers", 0, "app/handlers/**"),
		layerIntent("svc", "domain", 1, "app/domain/**"),
		facts.Fact{Kind: facts.KindModule, Repo: "svc", Name: "svc/app/handlers"},
		facts.Fact{Kind: facts.KindModule, Repo: "svc", Name: "svc/app/domain"},
		facts.Fact{Kind: facts.KindDependency, Repo: "svc", Name: "domain-imports-handlers",
			File:      "svc/app/domain/thing.go",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "svc/app/handlers"}}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var pattern, violation *facts.Insight
	for i := range insights {
		if strings.Contains(insights[i].Title, "declared (svc)") {
			pattern = &insights[i]
		}
		if strings.Contains(strings.ToLower(insights[i].Title), "violation") {
			violation = &insights[i]
		}
	}
	if pattern == nil || pattern.Confidence != 1.0 {
		t.Fatalf("declared pattern missing or not exact: %+v", insights)
	}
	if violation == nil || violation.Confidence != 1.0 {
		t.Fatalf("inner-imports-outer against a declaration must be a proof-class violation: %+v", insights)
	}
}

func TestDeclaredLayers_GlobDialectIsBounded(t *testing.T) {
	if !matchDeclaredLayerPath("app/handlers/api", []string{"app/handlers/**"}) {
		t.Fatal("subtree glob must match nested modules")
	}
	if !matchDeclaredLayerPath("app/handlers", []string{"app/handlers/**"}) {
		t.Fatal("subtree glob must match the prefix itself")
	}
	if matchDeclaredLayerPath("app/handlers2", []string{"app/handlers/**"}) {
		t.Fatal("prefix match must respect segment boundaries")
	}
	if !matchDeclaredLayerPath("lib/core", []string{"lib/core"}) {
		t.Fatal("exact form must match exactly")
	}
	if matchDeclaredLayerPath("lib/core/sub", []string{"lib/core"}) {
		t.Fatal("exact form must not match the subtree")
	}
}
