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

// A linter's rule, wrapped: a component selects lint facts by rule id and a
// forbid_fact rule verdicts every one, with the because-prose and the mode the
// declaration carries. The linter authors the rule; the graph gives it a
// baseline and a place in the one report.
func TestExplain_ForbidFactOverLintFactsWrapsALinterRule(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindIntent, Name: "component: mutating-args-warnings", File: "enola/constraints/eslint.yaml",
			Props: map[string]any{"intent_kind": "component", "component": "mutating-args-warnings",
				"match": "ember_app/**", "kind": "lint", "where": "lint_rule=tt/no-mutating-args", "source": "enola/constraints/eslint.yaml"}},
		facts.Fact{Kind: facts.KindIntent, Name: "rule: no-mutating-args", File: "enola/constraints/eslint.yaml",
			Props: map[string]any{"intent_kind": "rule", "rule": "no-mutating-args",
				"forbid_fact": "mutating-args-warnings", "mode": "ratchet",
				"because": "arguments are the caller's state", "source": "enola/constraints/eslint.yaml"}},
		facts.Fact{Kind: facts.KindLint, Name: "eslint: tt/no-mutating-args ember_app/app/components/a.gjs", File: "ember_app/app/components/a.gjs", Line: 12,
			Props: map[string]any{"lint_engine": "eslint", "lint_rule": "tt/no-mutating-args", "lint_severity": "warn", "resolution_level": "tool-reported"}},
		facts.Fact{Kind: facts.KindLint, Name: "eslint: tt/ember-declare-type ember_app/app/b.ts", File: "ember_app/app/b.ts", Line: 3,
			Props: map[string]any{"lint_engine": "eslint", "lint_rule": "tt/ember-declare-type", "lint_severity": "error", "resolution_level": "tool-reported"}},
		facts.Fact{Kind: facts.KindLint, Name: "eslint: tt/no-mutating-args lib/other/x.js", File: "lib/other/x.js", Line: 1,
			Props: map[string]any{"lint_engine": "eslint", "lint_rule": "tt/no-mutating-args", "lint_severity": "warn", "resolution_level": "tool-reported"}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var violations []facts.Insight
	for _, in := range insights {
		if strings.Contains(in.Title, "no-mutating-args violated") {
			violations = append(violations, in)
		}
	}
	if len(violations) != 1 {
		t.Fatalf("violations = %+v, want exactly the one matching rule inside ember_app/", violations)
	}
	if violations[0].Evidence[0].File != "ember_app/app/components/a.gjs" {
		t.Fatalf("evidence = %+v", violations[0].Evidence)
	}
}
