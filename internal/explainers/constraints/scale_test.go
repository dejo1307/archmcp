package constraints

// Scale bounds: how evaluation time grows with component size, and that the
// forbid_reach degrade fires exactly past its cap. The timings are measured
// and logged rather than asserted tightly — a wall-clock bound on shared
// hardware is a flake generator — with one deliberately generous ceiling so a
// complexity regression (seconds where milliseconds belong) still fails.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

// scaleStore builds a store with `size` members in each of two components
// and a declaration covering every direct form, plus forbid_reach when
// withReach is set. Each source member carries one calls edge into the target
// component, so verdict volume grows with the membership and the walk has
// real work.
func scaleStore(t *testing.T, size int, withReach bool) *facts.Store {
	t.Helper()
	var worldFacts []facts.Fact
	for i := 0; i < size; i++ {
		worldFacts = append(worldFacts,
			facts.Fact{
				Kind: facts.KindSymbol, Name: fmt.Sprintf("src.f%d", i),
				File:  fmt.Sprintf("src/f%d.x", i),
				Props: map[string]any{"exported": i%2 == 0},
				Relations: []facts.Relation{
					{Kind: facts.RelCalls, Target: fmt.Sprintf("dst.f%d", i)},
				},
			},
			facts.Fact{
				Kind: facts.KindSymbol, Name: fmt.Sprintf("dst.f%d", i),
				File:  fmt.Sprintf("dst/f%d.x", i),
				Props: map[string]any{"exported": true},
			},
		)
	}

	rules := []intent.ConstraintRule{
		{ID: "scale-forbid", Forbid: "src", To: "dst", Via: facts.RelCalls, Because: "scale"},
		{ID: "scale-allow", Allow: "src", Only: []string{"src"}, Via: facts.RelCalls, Because: "scale"},
		{ID: "scale-protect", Protect: "dst", Owners: []string{"dst"}, Via: facts.RelCalls, Because: "scale"},
		{ID: "scale-private", Private: "dst", Because: "scale"},
		{ID: "scale-cap", Cap: "src", MaxMembers: 1, Because: "scale"},
		{ID: "scale-name", RequireName: "src", Pattern: "src.*", Because: "scale"},
		{ID: "scale-edge", RequireEdge: "src", To: "dst", Via: facts.RelCalls, Direction: "inbound", Because: "scale"},
		{ID: "scale-protocol", Protocol: "src", Steps: []string{"src", "dst"}, Via: facts.RelCalls, Because: "scale"},
	}
	if withReach {
		rules = append(rules, intent.ConstraintRule{
			ID: "scale-reach", ForbidReach: "src", To: "dst", Because: "scale",
		})
	}
	d := &intent.Declaration{
		Components: []intent.ConstraintComponent{
			{Name: "src", Match: []string{"src/**"}},
			{Name: "dst", Match: []string{"dst/**"}},
		},
		Rules: rules,
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore()
	store.Add(intent.CompileFacts(d)...)
	store.Add(worldFacts...)
	return store
}

// directCeiling is deliberately generous: the direct forms at 500 members
// measure in the low milliseconds, and this only exists to catch a complexity
// regression, not scheduler noise.
const directCeiling = 5 * time.Second

func TestScale_DirectFormsStayFastAsComponentsGrow(t *testing.T) {
	for _, size := range []int{10, 100, 500} {
		store := scaleStore(t, size, false)
		start := time.Now()
		insights, err := New().Explain(context.Background(), store)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		// The forbid rule alone must verdict once per planted edge, so the
		// run demonstrably did size-proportional work.
		forbids, orphans, skippers := 0, 0, 0
		for _, in := range insights {
			if violationRule(in.Title) == "scale-forbid" {
				forbids++
			}
			if violationRule(in.Title) == "scale-edge" {
				orphans++
			}
			if violationRule(in.Title) == "scale-protocol" {
				skippers++
			}
		}
		if forbids != size {
			t.Fatalf("size %d: forbid verdicts = %d, want one per planted edge", size, forbids)
		}
		if orphans != size {
			t.Fatalf("size %d: existential verdicts = %d, want one per src member — dst calls none of them", size, orphans)
		}
		if skippers != size {
			t.Fatalf("size %d: protocol verdicts = %d, want one per src member — each reaches step dst without step src", size, skippers)
		}
		if elapsed > directCeiling {
			t.Fatalf("size %d: direct forms took %s, over the %s ceiling", size, elapsed, directCeiling)
		}
		t.Logf("direct forms at %4d members: %s (%d insights)", size, elapsed, len(insights))
	}
}

func TestScale_ForbidReachWithinCapWalksAndVerdicts(t *testing.T) {
	store := scaleStore(t, reachComponentCap, true)
	start := time.Now()
	insights, err := New().Explain(context.Background(), store)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	reaches := 0
	for _, in := range insights {
		if violationRule(in.Title) == "scale-reach" {
			reaches++
		}
	}
	if reaches != reachComponentCap {
		t.Fatalf("reach verdicts = %d, want one per (source, target) pair at the cap", reaches)
	}
	t.Logf("forbid_reach at the %d-member cap: %s", reachComponentCap, elapsed)
}

func TestScale_ForbidReachDegradePastTheCap(t *testing.T) {
	store := scaleStore(t, reachComponentCap+1, true)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	skip := false
	for _, in := range insights {
		if violationRule(in.Title) == "scale-reach" {
			t.Fatalf("a membership past the cap must not verdict, got: %s", in.Title)
		}
		if strings.HasPrefix(in.Title, "forbid_reach rule scale-reach skipped") {
			skip = true
			if in.Confidence != reachSkipConfidence {
				t.Fatalf("skip advisory at %v, want %v", in.Confidence, reachSkipConfidence)
			}
		}
	}
	if !skip {
		t.Fatal("the honest degrade left no visible trace: no skip advisory for the oversized rule")
	}
}
