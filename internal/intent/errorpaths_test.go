package intent

// Hostile-input coverage for the declaration parser. A declaration is the one
// input a repository's whole verdict set is computed from, so every failure
// here must be a NAMED error — never a panic, never a silent skip, and never
// an unbounded expansion. The cases pin the boundary: what parses cleanly,
// what errors and says why, and what yaml.v3's own alias ceiling refuses.

import (
	"fmt"
	"strings"
	"testing"
)

// aliasBomb builds a billion-laughs document whose expansion is nine levels
// deep. The final key decides where the bomb lands: an unknown key is skipped
// without expansion, a known one forces the decoder to expand into the
// declaration's own shape.
func aliasBomb(finalKey string) string {
	var b strings.Builder
	b.WriteString("a0: &a0 [x,x,x,x,x,x,x,x,x]\n")
	for i := 1; i < 9; i++ {
		fmt.Fprintf(&b, "a%d: &a%d [", i, i)
		for j := 0; j < 9; j++ {
			if j > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	fmt.Fprintf(&b, "%s: [*a8,*a8,*a8]\n", finalKey)
	return b.String()
}

func TestParse_HostileInputsErrorByName(t *testing.T) {
	hugeToken := strings.Repeat("a", 1<<20)
	cases := map[string]struct {
		input   string
		wantErr string // empty means the input must parse cleanly
	}{
		// The exponential payload cannot fit the declaration's typed shape, so
		// the decoder refuses it at the first alias whose value does not match
		// the field's type — a named error before any expansion, which is the
		// bound this case pins.
		"alias bomb in a known field is refused by type, never expanded": {
			input:   aliasBomb("layers"),
			wantErr: "cannot unmarshal",
		},
		"alias bomb under unknown keys is skipped unexpanded": {
			input: aliasBomb("unknown"),
		},
		"wrong type for a section": {
			input:   "consumes: just a string\n",
			wantErr: "cannot unmarshal",
		},
		"wrong type inside a component": {
			input:   "components:\n  - name: [not, a, string]\n",
			wantErr: "cannot unmarshal",
		},
		"huge lowercase token is still validated by shape": {
			input:   "components:\n  - name: " + hugeToken + "\n",
			wantErr: "needs at least one match pattern",
		},
		"huge via names the allowed set": {
			input:   "consumes:\n  - repo: billing\n    via: " + hugeToken + "\n",
			wantErr: "is not a linker mechanism",
		},
		"pathological glob star-star bare": {
			input:   "components:\n  - name: c\n    match: [\"**\"]\n",
			wantErr: "must be an exact path, a prefix/** subtree, or a **/name basename glob",
		},
		"pathological glob character class": {
			input:   "components:\n  - name: c\n    match: [\"app/[a-z]/**\"]\n",
			wantErr: "must be an exact path, a prefix/** subtree, or a **/name basename glob",
		},
		"pathological glob brace set": {
			input:   "components:\n  - name: c\n    match: [\"app/{a,b}\"]\n",
			wantErr: "must be an exact path, a prefix/** subtree, or a **/name basename glob",
		},
		"basename glob selecting every name": {
			input:   "components:\n  - name: c\n    match: [\"**/*\"]\n",
			wantErr: "at most one * around a non-empty literal",
		},
		"basename glob with a second star": {
			input:   "components:\n  - name: c\n    match: [\"**/*_controller*.js\"]\n",
			wantErr: "at most one * around a non-empty literal",
		},
		"basename glob spanning a directory": {
			input:   "components:\n  - name: c\n    match: [\"**/controllers/*.js\"]\n",
			wantErr: "at most one * around a non-empty literal",
		},
		"basename glob with a character class": {
			input:   "components:\n  - name: c\n    match: [\"**/[a-z]_controller.js\"]\n",
			wantErr: "at most one * around a non-empty literal",
		},
		"basename glob with an escape": {
			input:   "components:\n  - name: c\n    match: [\"**/\\\\*.js\"]\n",
			wantErr: "at most one * around a non-empty literal",
		},
		"basename glob with a subtree tail": {
			input:   "components:\n  - name: c\n    match: [\"**/controllers/**\"]\n",
			wantErr: "at most one * around a non-empty literal",
		},
		"basename glob is well formed and parses": {
			input: "components:\n  - name: c\n    match: [\"**/*_controller.js\"]\n",
		},
		"basename with no star is well formed and parses": {
			input: "components:\n  - name: c\n    match: [\"**/Gemfile\"]\n",
		},
		"pathological name pattern double star": {
			input:   "components:\n  - name: c\n    match: [app/**]\nrules:\n  - id: r\n    require_name: c\n    pattern: \"**\"\n    because: x\n",
			wantErr: "pattern that is an exact name",
		},
		"duplicate rule ids in one declaration": {
			input:   "components:\n  - name: c\n    match: [app/**]\nrules:\n  - id: twice\n    forbid_fact: c\n    because: x\n  - id: twice\n    cap: c\n    max_members: 3\n    because: x\n",
			wantErr: "declared twice",
		},
		"empty declaration declares nothing and is not an error": {
			input: "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d, err := Parse([]byte(tc.input))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want a clean parse, got: %v", err)
				}
				if d == nil {
					t.Fatal("a clean parse must return a declaration")
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error naming %q, got a declaration: %+v", tc.wantErr, d)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error must name the problem %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

// An enormous declaration must come back with every problem named rather than
// hanging, panicking, or reporting only the first: Problems is the surface
// `constraints lint` renders per line, so the whole census matters.
func TestParse_EnormousDeclarationNamesEveryProblem(t *testing.T) {
	var b strings.Builder
	b.WriteString("components:\n  - name: c\n    match: [app/**]\nrules:\n")
	const rules = 2000
	for i := 0; i < rules; i++ {
		fmt.Fprintf(&b, "  - id: r%d\n    forbid_fact: c\n", i)
	}
	_, err := Parse([]byte(b.String()))
	if err == nil {
		t.Fatal("every rule is missing its because; the parse must fail")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("(r%d)", rules-1)) {
		t.Fatalf("the error must reach the last rule's problem, got %d bytes of error", len(err.Error()))
	}
}

// The cluster config overrides a repo file wholesale, so two declarations
// carrying the same rule id never merge into a duplicate: the resolved
// declaration is the cluster's alone, and the override is recorded.
func TestResolve_SameRuleIDAcrossFileAndClusterNeverMerges(t *testing.T) {
	file := &Declaration{
		Components: []ConstraintComponent{{Name: "c", Match: []string{"app/**"}}},
		Rules:      []ConstraintRule{{ID: "shared-id", ForbidFact: "c", Because: "from the file"}},
		Source:     RepoFileName,
	}
	cluster := &Declaration{
		Components: []ConstraintComponent{{Name: "c", Match: []string{"lib/**"}}},
		Rules:      []ConstraintRule{{ID: "shared-id", Cap: "c", MaxMembers: 3, Because: "from the cluster"}},
	}
	resolved := Resolve(file, cluster)
	if !resolved.Overridden || resolved.Source != ClusterSource {
		t.Fatalf("override not recorded: %+v", resolved)
	}
	if len(resolved.Rules) != 1 || resolved.Rules[0].Because != "from the cluster" {
		t.Fatalf("resolution must be wholesale, got %+v", resolved.Rules)
	}
	if problems := resolved.Problems(); len(problems) > 0 {
		t.Fatalf("the resolved declaration must validate on its own: %v", problems)
	}
}

// An exemplar reaches matchConstraintPath without passing validConstraintMatch,
// so the basename glob would change what a guidance rule points at while the
// declaration screen said nothing. The prefix is refused rather than honoured.
func TestExemplarRefusesTheBasenameGlobPrefix(t *testing.T) {
	d := &Declaration{
		Components: []ConstraintComponent{{Name: "views", Match: []string{"app/views/**"}}},
		Rules: []ConstraintRule{{
			ID: "prefer-the-component", Guide: "views", Message: "reach for the component",
			Exemplars: []string{"**/*_controller.js"}, Because: "prior art reads better",
		}},
	}
	var found bool
	for _, p := range d.Problems() {
		if strings.Contains(p, "names prior art, not a pattern") {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %v, want the exemplar refused for carrying the basename-glob prefix", d.Problems())
	}

	d.Rules[0].Exemplars = []string{"app/components/card_component.rb"}
	for _, p := range d.Problems() {
		if strings.Contains(p, "names prior art, not a pattern") {
			t.Errorf("a literal exemplar was refused: %s", p)
		}
	}
}
