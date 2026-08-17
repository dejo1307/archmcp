package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/providers"
)

func declaredContract(receiver, method, signature, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: "rbs-signature: " + receiver + "#" + method,
		File: file,
		Props: map[string]any{
			providers.PropResolutionLevel: providers.LevelDeclared,
			providers.PropDeclaredIn:      file,
			"receiver":                    receiver,
			"method":                      method,
			"singleton":                   false,
			"signature":                   signature,
		}}
}

func TestExplain_ForbidFactVerdictCitesTheDeclarationFile(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindIntent, Name: "component: retired-contracts", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "component", "component": "retired-contracts",
				"match": "sig/**", "kind": "symbol",
				"name_pattern": "rbs-signature: Legacy::Export#run", "source": "wiki/p.md"}},
		facts.Fact{Kind: facts.KindIntent, Name: "rule: no-retired-contracts", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "no-retired-contracts",
				"forbid_fact": "retired-contracts",
				"because":     "the Legacy::Export API was retired; its signature file must not keep declaring the contract",
				"source":      "wiki/p.md"}},
		declaredContract("Legacy::Export", "run", "() -> void", "sig/legacy.rbs"),
		declaredContract("Billing::Ledger", "record", "(Invoice invoice) -> String", "sig/billing.rbs"),
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	want := "Constraint no-retired-contracts violated: rbs-signature: Legacy::Export#run is measured in retired-contracts"
	if got.Title != want {
		t.Errorf("title = %q, want %q", got.Title, want)
	}
	if got.Confidence != 1.0 {
		t.Errorf("confidence = %v, want 1.0: the rule is declared and the contract is measured", got.Confidence)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].File != "sig/legacy.rbs" ||
		got.Evidence[0].Symbol != "rbs-signature: Legacy::Export#run" {
		t.Errorf("evidence = %+v, want the declared contract and its signature file", got.Evidence)
	}
}

func TestExplain_RequireVerdictsOverTheTypedAnnotation(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindIntent, Name: "component: billing-api", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "component", "component": "billing-api",
				"match": "app/models/billing/**", "kind": "symbol", "source": "wiki/p.md"}},
		facts.Fact{Kind: facts.KindIntent, Name: "rule: billing-api-is-typed", File: "wiki/p.md",
			Props: map[string]any{"intent_kind": "rule", "rule": "billing-api-is-typed",
				"require": "billing-api", "must_prop": providers.PropDeclaredIn, "must_value": "sig/billing.rbs",
				"because": "every billing symbol must carry a declared contract from the billing signature file",
				"source":  "wiki/p.md"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing::Ledger#record", File: "app/models/billing/ledger.rb",
			Props: map[string]any{
				providers.PropTyped:             true,
				providers.PropDeclaredSignature: "(Invoice invoice) -> String",
				providers.PropDeclaredIn:        "sig/billing.rbs"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing::Ledger#drain", File: "app/models/billing/ledger.rb",
			Props: map[string]any{}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(insights) != 1 {
		t.Fatalf("insights = %d, want exactly 1: %+v", len(insights), insights)
	}
	got := insights[0]
	if !strings.Contains(got.Title, "Billing::Ledger#drain must have declared_in containing sig/billing.rbs") {
		t.Errorf("title = %q, want the undeclared symbol named", got.Title)
	}
	if strings.Contains(got.Title, "Billing::Ledger#record") {
		t.Errorf("title = %q: the typed symbol satisfies the rule and must stay silent", got.Title)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Symbol != "Billing::Ledger#drain" {
		t.Errorf("evidence = %+v", got.Evidence)
	}
}
