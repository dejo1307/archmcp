package intent

import (
	"testing"
)

// FuzzParse drives the declaration parser with arbitrary bytes. The contract
// under fuzz is the fail-closed one: any input either parses into a
// declaration the validator has no problem with, or returns a named error —
// never a panic, and never a declaration that half-passed validation, because
// every verdict about a repo is computed from what this function returns.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"",
		"service:\n  name: payments\nconsumes:\n  - repo: billing\n    via: http-client\nserves:\n  - via: http\nlayers:\n  - name: handlers\n    paths: [\"app/controllers/**\"]\n",
		"components:\n  - name: domain\n    match: [\"app/domain/**\"]\n  - name: adapters\n    match: [\"app/adapters/**\"]\nrules:\n  - id: domain-stays-pure\n    forbid: domain\n    to: adapters\n    via: depends_on\n    because: the domain must not know its delivery mechanisms\n",
		"rules:\n  - id: r\n    require: tables\n    when_prop_contains:\n      prop: columns\n      value: company_id\n    must_prop_contain:\n      prop: fk_constraints\n      value: company_id->companies\n    because: x\n",
		"rules:\n  - id: g\n    guide: domain\n    message: prior art exists\n    exemplars: [app/domain/billing.rb]\n    mode: notify\n    because: steering\n",
		"components:\n  - name: callers\n    match: [\"app/checkout/**\"]\n  - name: validate\n    match: [\"app/steps/validate/**\"]\n  - name: charge\n    match: [\"app/steps/charge/**\"]\nrules:\n  - id: order\n    protocol: callers\n    steps: [validate, charge]\n    via: calls\n    because: charging without validating charges garbage\n",
		"consumes:\n  - repo: billing\n    via: rest\n",
		"components:\n  - name: domain\n",
		"layers:\n  - name: handlers\n",
		"a: &a [x, x]\nb: &b [*a, *a]\nc: [*b, *b]\n",
		"components:\n  - name: [not, a, string]\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := Parse(data)
		if err != nil {
			if d != nil {
				t.Fatalf("a failed parse must not also return a declaration: %+v", d)
			}
			return
		}
		if d == nil {
			t.Fatal("a successful parse must return a declaration")
		}
		if problems := d.Problems(); len(problems) > 0 {
			t.Fatalf("Parse validated this declaration and Problems still reports: %v", problems)
		}
		CompileFacts(d)
	})
}
