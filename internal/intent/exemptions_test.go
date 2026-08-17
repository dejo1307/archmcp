package intent

import (
	"strings"
	"testing"
)

func exemptedRule(exempt ...ConstraintExemption) []ConstraintRule {
	return []ConstraintRule{{
		ID:      "domain-stays-pure",
		Forbid:  "domain",
		To:      "adapters",
		Via:     "depends_on",
		Because: "the domain must not know its delivery mechanisms",
		Exempt:  exempt,
	}}
}

func exemptionComponents() []ConstraintComponent {
	return []ConstraintComponent{
		{Name: "domain", Match: []string{"app/domain/**"}},
		{Name: "adapters", Match: []string{"app/adapters/**"}},
	}
}

func fullExemption() ConstraintExemption {
	return ConstraintExemption{
		Witness: "app/domain/billing -> app/adapters/http via depends_on",
		Owner:   "alice",
		Because: "the legacy billing adapter migrates in Q4",
		Since:   "2026-08-10",
	}
}

func TestDeclarationValidate_ExemptionAccepted(t *testing.T) {
	decl := constraintDecl(exemptionComponents(), exemptedRule(fullExemption()))
	if err := decl.Validate(); err != nil {
		t.Fatalf("a fully signed exemption must validate, got: %v", err)
	}
}

func TestDeclarationValidate_ExemptionProblems(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*ConstraintExemption)
		wantIn string
	}{
		"missing witness rejected":  {func(ex *ConstraintExemption) { ex.Witness = "" }, "missing witness"},
		"missing owner rejected":    {func(ex *ConstraintExemption) { ex.Owner = " " }, "missing owner"},
		"missing because rejected":  {func(ex *ConstraintExemption) { ex.Because = "" }, "missing because"},
		"missing since rejected":    {func(ex *ConstraintExemption) { ex.Since = "" }, "YYYY-MM-DD"},
		"free-form since rejected":  {func(ex *ConstraintExemption) { ex.Since = "last spring" }, "YYYY-MM-DD"},
		"month-only since rejected": {func(ex *ConstraintExemption) { ex.Since = "2026-08" }, "YYYY-MM-DD"},
	} {
		t.Run(name, func(t *testing.T) {
			ex := fullExemption()
			tc.mutate(&ex)
			err := constraintDecl(exemptionComponents(), exemptedRule(ex)).Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("want an error containing %q, got: %v", tc.wantIn, err)
			}
			if err != nil && !strings.Contains(err.Error(), "exempt[0]") {
				t.Errorf("the error must locate the entry, got: %v", err)
			}
		})
	}
}

func TestDeclarationValidate_DuplicateExemptionWitnessRejected(t *testing.T) {
	err := constraintDecl(exemptionComponents(), exemptedRule(fullExemption(), fullExemption())).Validate()
	if err == nil || !strings.Contains(err.Error(), "already exempted") {
		t.Errorf("two exemptions on one witness have no single answer, got: %v", err)
	}
}

func TestDeclarationValidate_ExemptOnGuidanceRejected(t *testing.T) {
	rules := []ConstraintRule{{
		ID:      "getters-cached",
		Guide:   "domain",
		Message: "consider caching",
		Because: "recomputation is the recurring bug",
		Exempt:  []ConstraintExemption{fullExemption()},
	}}
	err := constraintDecl(exemptionComponents(), rules).Validate()
	if err == nil || !strings.Contains(err.Error(), "guidance emits no violations") {
		t.Errorf("guidance has nothing to exempt, got: %v", err)
	}
}

func TestCompileFacts_ExemptPropIsSortedAndRoundTrips(t *testing.T) {
	second := ConstraintExemption{
		Witness: "app/domain/pricing -> app/adapters/queue via depends_on",
		Owner:   "bob",
		Because: "the queue adapter is grandfathered until the broker swap",
		Since:   "2026-07-01",
	}
	forward := CompileFacts(constraintDecl(exemptionComponents(), exemptedRule(fullExemption(), second)))
	reversed := CompileFacts(constraintDecl(exemptionComponents(), exemptedRule(second, fullExemption())))
	forwardProp := forward[len(forward)-1].PropString("exempt")
	reversedProp := reversed[len(reversed)-1].PropString("exempt")
	if forwardProp == "" || forwardProp != reversedProp {
		t.Fatalf("the compiled exempt prop must be a function of the declared set, not YAML order:\n%q\n%q", forwardProp, reversedProp)
	}
	decoded := DecodeExemptions(forwardProp)
	if len(decoded) != 2 || decoded[0] != fullExemption() || decoded[1] != second {
		t.Errorf("decoded = %+v, want both entries in witness order", decoded)
	}
}

func TestCompileFacts_NoExemptionsCompileNoProp(t *testing.T) {
	ff := CompileFacts(constraintDecl(exemptionComponents(), exemptedRule()))
	if _, present := ff[len(ff)-1].Props["exempt"]; present {
		t.Errorf("a rule with no exemptions must compile no exempt prop: %+v", ff[len(ff)-1].Props)
	}
}

func TestDecodeExemptions_FailsClosed(t *testing.T) {
	if got := DecodeExemptions(""); got != nil {
		t.Errorf("empty prop = %+v, want nil", got)
	}
	if got := DecodeExemptions("not json"); got != nil {
		t.Errorf("unreadable prop = %+v, want nil — a carve-out the evaluator cannot read must never silence a violation", got)
	}
}

const constraintsExemptedBillingFile = `
components:
  - name: billing
    match: ["app/billing/**"]
rules:
  - id: billing-owned
    protect: billing
    owners: [domain]
    via: calls
    because: only the domain may drive billing
    exempt:
      - witness: "app/legacy/export -> app/billing/ledger via calls"
        owner: alice
        because: the export path retires with the Q4 ledger rewrite
        since: "2026-08-10"
`

const constraintsBrokenExemptionFile = `
components:
  - name: adapters
    match: ["app/adapters/**"]
rules:
  - id: adapters-capped
    cap: adapters
    max_members: 5
    because: the adapter surface stays small
    exempt:
      - witness: "adapters has 6 members over a cap of 5"
        because: inherited overflow, shrinking release by release
        since: "2026-08-10"
`

func TestLoadRepoFile_ExemptionsMergeFromConstraintsFiles(t *testing.T) {
	dir := writeConstraintsRepo(t, constraintsInline, map[string]string{
		"adapters.yaml": constraintsAdaptersFile,
		"billing.yaml":  constraintsExemptedBillingFile,
	})
	d, err := LoadRepoFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("the merged declaration must validate, got: %v", err)
	}
	var billingRule *ConstraintRule
	for i := range d.Rules {
		if d.Rules[i].ID == "billing-owned" {
			billingRule = &d.Rules[i]
		}
	}
	if billingRule == nil || len(billingRule.Exempt) != 1 {
		t.Fatalf("rules = %+v, want billing-owned carrying its exemption through the merge", d.Rules)
	}
	if billingRule.SourceFile != ConstraintsDirName+"/billing.yaml" {
		t.Errorf("source = %q, want the declaring constraints file", billingRule.SourceFile)
	}
	if billingRule.Exempt[0].Owner != "alice" {
		t.Errorf("exemption = %+v, want alice's signed entry verbatim", billingRule.Exempt[0])
	}
}

func TestLoadRepoFile_ExemptionProblemCitesTheDeclaringFile(t *testing.T) {
	dir := writeConstraintsRepo(t, "", map[string]string{
		"adapters.yaml": constraintsBrokenExemptionFile,
	})
	_, err := LoadRepoFile(dir)
	if err == nil || !strings.Contains(err.Error(), ConstraintsDirName+"/adapters.yaml") || !strings.Contains(err.Error(), "missing owner") {
		t.Errorf("want the ownerless exemption rejected under its declaring file, got: %v", err)
	}
}
