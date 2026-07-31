package godclass

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// ringGraph builds a symbol graph with a realistic fan-in distribution: n symbols
// arranged in a ring so every one has at least one caller, plus extra callers drawn
// from the same pool so fan-in spreads across 1..mod, and one hub well above the rest.
//
// Drawing the extra callers from the pool rather than inventing new ones is what
// makes the fixture faithful. A generator that adds a fresh caller symbol per edge
// floods the distribution with zero-fan-in symbols, which drags mean+kσ far below
// the hub — the confidence then saturates at exactly 1.0 and the test asserts
// nothing at all, however carefully it compares. (Ask me how I know.)
func ringGraph(n, mod, hubExtra int) []facts.Fact {
	name := func(i int) string { return fmt.Sprintf("pkg/s%d.Fn", i%n) }

	targets := make([][]string, n)
	for i := range n {
		targets[i] = append(targets[i], name(i+1))
	}
	for j := range n {
		extra := j % mod
		if j == 0 {
			extra = hubExtra
		}
		for k := range extra {
			c := (j + 2 + k) % n
			targets[c] = append(targets[c], name(j))
		}
	}

	out := make([]facts.Fact, 0, n)
	for i := range n {
		var rels []facts.Relation
		for _, t := range targets[i] {
			rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: t})
		}
		out = append(out, facts.Fact{
			Kind:      facts.KindSymbol,
			Name:      name(i),
			File:      fmt.Sprintf("pkg/s%d.go", i),
			Relations: rels,
		})
	}
	return out
}

func storeInOrder(symbols []facts.Fact) *facts.Store {
	s := facts.NewStore()
	s.Add(symbols...)
	s.BuildGraph()
	return s
}

// God-class is the only explainer that puts a computed threshold into its OUTPUT
// rather than using it solely to select, so it must produce identical output for
// identical inputs whatever order the fact store happens to hold them in.
//
// That order is not stable: it reflects concurrent extraction and varies between
// runs of an unchanged tree. Across 90 runs of 30 repositories, facts.jsonl was
// byte-identical every time — it is sorted before it is written — while
// insights.json differed on one repository, 11 of 25 findings, spread ~1.9e-15,
// because `mean + kσ` was reduced with sequential `+=` over a slice in store order
// and float addition is not associative. Nothing a human reads changed; the
// receipt's output_hashes["insights.json"] stopped being reproducible.
//
// Bitwise comparison is the point. An epsilon would pass on the broken code: the
// whole defect lives below any tolerance anyone would pick, and above the zero that
// byte-comparing an artifact demands.
func TestExplain_ConfidenceDoesNotDependOnFactStoreOrder(t *testing.T) {
	base := ringGraph(100, 10, 20)

	want, err := New().Explain(context.Background(), storeInOrder(base))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("fixture produced no god-class findings, so this test would assert nothing")
	}
	// The guard that this test needs most. `confidence` clamps to [0.5, 1], so a hub
	// far enough above the threshold reports exactly 1.0 no matter how the threshold
	// wobbles — and the comparison below then passes on broken and fixed code alike.
	// The real finding sat at 0.869; this fixture sits at 0.864.
	for _, in := range want {
		if in.Confidence <= 0.5 || in.Confidence >= 1 {
			t.Fatalf("confidence %v is clamped, so the threshold cannot influence it and "+
				"this test proves nothing — retune the fixture", in.Confidence)
		}
	}

	r := rand.New(rand.NewSource(7))
	for i := range 50 {
		shuffled := append([]facts.Fact(nil), base...)
		r.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		got, err := New().Explain(context.Background(), storeInOrder(shuffled))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("shuffle %d produced %d findings, want %d", i, len(got), len(want))
		}
		for j := range got {
			if got[j].Title != want[j].Title {
				t.Fatalf("shuffle %d: finding %d is %q, want %q — the finding ORDER depends on "+
					"store order too", i, j, got[j].Title, want[j].Title)
			}
			if got[j].Confidence != want[j].Confidence {
				t.Fatalf("shuffle %d: confidence for %q is %.17g, want %.17g — insights.json is "+
					"not byte-reproducible", i, got[j].Title, got[j].Confidence, want[j].Confidence)
			}
		}
	}
}
