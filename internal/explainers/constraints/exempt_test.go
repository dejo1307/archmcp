package constraints

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

func exemptRuleIntent(id, forbid, to, via, mode string, exempt ...intent.ConstraintExemption) facts.Fact {
	props := map[string]any{"intent_kind": "rule", "rule": id, "forbid": forbid, "to": to,
		"via": via, "because": "the domain must not know its delivery mechanisms", "source": "enola/constraints/domain.yaml"}
	if mode != "" {
		props["mode"] = mode
	}
	if encoded := intent.EncodeExemptions(exempt); encoded != "" {
		props["exempt"] = encoded
	}
	return facts.Fact{Kind: facts.KindIntent, Name: "rule: " + id, File: "enola/constraints/domain.yaml", Props: props}
}

func billingExemption() intent.ConstraintExemption {
	return intent.ConstraintExemption{
		Witness: "app/domain/billing -> app/adapters/http via depends_on",
		Owner:   "alice",
		Because: "the legacy billing adapter migrates in Q4",
		Since:   "2026-08-10",
	}
}

func exemptStore(mode string, exempt ...intent.ConstraintExemption) *facts.Store {
	store := facts.NewStore()
	store.Add(
		componentIntent("domain", "app/domain/**"),
		componentIntent("adapters", "app/adapters/**"),
		exemptRuleIntent("domain-stays-pure", "domain", "adapters", "depends_on", mode, exempt...),
		facts.Fact{Kind: facts.KindModule, Name: "app/domain/billing", File: "app/domain/billing",
			Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: "app/adapters/http"}}},
		facts.Fact{Kind: facts.KindModule, Name: "app/domain/pricing", File: "app/domain/pricing",
			Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: "app/adapters/queue"}}},
		facts.Fact{Kind: facts.KindModule, Name: "app/adapters/http", File: "app/adapters/http"},
		facts.Fact{Kind: facts.KindModule, Name: "app/adapters/queue", File: "app/adapters/queue"},
	)
	return store
}

func TestExplain_ExemptedWitnessIsNoViolationInAnyMode(t *testing.T) {
	for _, mode := range []string{"", "advisory", "strict"} {
		t.Run("mode "+mode, func(t *testing.T) {
			insights, err := New().Explain(context.Background(), exemptStore(mode, billingExemption()))
			if err != nil {
				t.Fatal(err)
			}
			var exempted, violations []facts.Insight
			for _, in := range insights {
				if strings.HasPrefix(in.Title, "Exempted from constraint ") {
					exempted = append(exempted, in)
				}
				if strings.Contains(in.Title, "violated") {
					violations = append(violations, in)
				}
			}
			if len(violations) != 1 || !strings.Contains(violations[0].Title, "app/domain/pricing -> app/adapters/queue") {
				t.Fatalf("violations = %+v, want exactly the non-exempted witness still reported", violations)
			}
			if len(exempted) != 1 {
				t.Fatalf("exempted = %+v, want exactly one exempted entry", exempted)
			}
			got := exempted[0]
			want := "Exempted from constraint domain-stays-pure: app/domain/billing -> app/adapters/http via depends_on"
			if got.Title != want {
				t.Errorf("title = %q, want %q", got.Title, want)
			}
			if got.Confidence != exemptedConfidence {
				t.Errorf("confidence = %v, want %v — counted and visible, never gating", got.Confidence, exemptedConfidence)
			}
			for _, part := range []string{"alice", "2026-08-10", "the legacy billing adapter migrates in Q4"} {
				if !strings.Contains(got.Description, part) {
					t.Errorf("description must carry %q, got: %q", part, got.Description)
				}
			}
			if len(got.Evidence) == 0 || !strings.Contains(got.Evidence[0].Detail, "exempted by alice since 2026-08-10") {
				t.Errorf("evidence = %+v, want the signature leading it", got.Evidence)
			}
		})
	}
}

func TestExplain_ExemptedRequireWitness(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindIntent, Name: "component: tables", File: "enola/constraints/tenancy.yaml",
			Props: map[string]any{"intent_kind": "component", "component": "tables", "match": "db/**", "kind": "storage", "source": "enola/constraints/tenancy.yaml"}},
		facts.Fact{Kind: facts.KindIntent, Name: "rule: company-fk", File: "enola/constraints/tenancy.yaml",
			Props: map[string]any{"intent_kind": "rule", "rule": "company-fk", "require": "tables",
				"when_prop": "columns", "when_value": "company_id",
				"must_prop": "fk_constraints", "must_value": "company_id->companies",
				"because": "tenant isolation joins through companies", "source": "enola/constraints/tenancy.yaml",
				"exempt": intent.EncodeExemptions([]intent.ConstraintExemption{{
					Witness: "legacy_imports must have fk_constraints containing company_id->companies",
					Owner:   "dana",
					Because: "legacy_imports keys company_id to the archived companies snapshot, not companies",
					Since:   "2026-08-01",
				}})}},
		facts.Fact{Kind: facts.KindStorage, Name: "legacy_imports", File: "db/structure.sql",
			Props: map[string]any{"columns": "id company_id payload"}},
		facts.Fact{Kind: facts.KindStorage, Name: "invoices", File: "db/structure.sql",
			Props: map[string]any{"columns": "id company_id total"}},
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, in := range insights {
		titles = append(titles, in.Title)
	}
	wantViolation := "Constraint company-fk violated: invoices must have fk_constraints containing company_id->companies"
	wantExempted := "Exempted from constraint company-fk: legacy_imports must have fk_constraints containing company_id->companies"
	if len(insights) != 2 || titles[0] != wantViolation || titles[1] != wantExempted {
		t.Errorf("titles = %q, want the un-exempted table failing and the exempted one recorded", titles)
	}
}

func TestExplain_DeadExemptionWarns(t *testing.T) {
	dead := intent.ConstraintExemption{
		Witness: "app/domain/reporting -> app/adapters/http via depends_on",
		Owner:   "alice",
		Because: "reporting was excused during the extraction",
		Since:   "2026-05-01",
	}
	insights, err := New().Explain(context.Background(), exemptStore("", dead))
	if err != nil {
		t.Fatal(err)
	}
	var warning *facts.Insight
	for i := range insights {
		if strings.HasPrefix(insights[i].Title, "Constraint exemption on ") {
			warning = &insights[i]
		}
	}
	if warning == nil {
		t.Fatalf("insights = %+v, want a dead-exemption warning", insights)
	}
	want := "Constraint exemption on domain-stays-pure matches nothing: app/domain/reporting -> app/adapters/http via depends_on"
	if warning.Title != want {
		t.Errorf("title = %q, want %q", warning.Title, want)
	}
	if warning.Confidence != deadExemptionConfidence {
		t.Errorf("confidence = %v, want %v — a warning, like the dead-selector advisory", warning.Confidence, deadExemptionConfidence)
	}
	if !strings.Contains(warning.Description, "outlived") {
		t.Errorf("description must say the exemption may have outlived its violation, got: %q", warning.Description)
	}
	for _, in := range insights {
		if strings.Contains(in.Title, "reporting") && strings.Contains(in.Title, "violated") {
			t.Errorf("a dead exemption must not invent a violation: %q", in.Title)
		}
	}
}

func TestExplain_ExemptionsAreDeterministic(t *testing.T) {
	first, err := New().Explain(context.Background(), exemptStore("strict", billingExemption(), intent.ConstraintExemption{
		Witness: "app/domain/ghost -> app/adapters/http via depends_on",
		Owner:   "bob",
		Because: "ghost never existed",
		Since:   "2026-01-01",
	}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := New().Explain(context.Background(), exemptStore("strict", billingExemption(), intent.ConstraintExemption{
		Witness: "app/domain/ghost -> app/adapters/http via depends_on",
		Owner:   "bob",
		Because: "ghost never existed",
		Since:   "2026-01-01",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two runs over the same store must agree:\n%+v\n%+v", first, second)
	}
}
