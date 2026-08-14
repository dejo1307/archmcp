package diff

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// Counts exists to produce the same numbers as Compute for a fraction of the memory,
// so the only test that matters is whether it does. Compute is the oracle throughout:
// these tests never assert a hand-written expected count, because a hand-written count
// would be asserting my reading of Compute rather than Compute.

// factsOnly wraps facts as a snapshot. Named to avoid diff_test.go's snap(),
// which takes facts AND insights.
func factsOnly(ff []facts.Fact) *facts.Snapshot { return &facts.Snapshot{Facts: ff} }

// assertAgrees fails with the discrepancy when Counts and Compute disagree.
func assertAgrees(t *testing.T, baseline, current []facts.Fact) {
	t.Helper()
	want := Compute(factsOnly(baseline), factsOnly(current))
	got := CountSnapshots(baseline, current)

	if got.FactsAdded != len(want.FactsAdded) {
		t.Errorf("FactsAdded = %d, Compute says %d", got.FactsAdded, len(want.FactsAdded))
	}
	if got.FactsRemoved != len(want.FactsRemoved) {
		t.Errorf("FactsRemoved = %d, Compute says %d", got.FactsRemoved, len(want.FactsRemoved))
	}
	if got.FactsChanged != len(want.FactsChanged) {
		t.Errorf("FactsChanged = %d, Compute says %d", got.FactsChanged, len(want.FactsChanged))
	}
	if got.EdgesAdded != len(want.EdgesAdded) {
		t.Errorf("EdgesAdded = %d, Compute says %d", got.EdgesAdded, len(want.EdgesAdded))
	}
	if got.EdgesRemoved != len(want.EdgesRemoved) {
		t.Errorf("EdgesRemoved = %d, Compute says %d", got.EdgesRemoved, len(want.EdgesRemoved))
	}
	assertKindsAgree(t, "added", got.AddedByKind, KindCounts(want.FactsAdded))
	assertKindsAgree(t, "removed", got.RemovedByKind, KindCounts(want.FactsRemoved))
}

func assertKindsAgree(t *testing.T, label string, got, want map[string]int) {
	t.Helper()
	for k, n := range want {
		if got[k] != n {
			t.Errorf("%s[%s] = %d, Compute says %d", label, k, got[k], n)
		}
	}
	for k, n := range got {
		if want[k] != n {
			t.Errorf("%s[%s] = %d, Compute says %d", label, k, n, want[k])
		}
	}
}

func TestCounts_MatchesCompute_Shapes(t *testing.T) {
	sym := func(name, file string, line int, props map[string]any, rels ...facts.Relation) facts.Fact {
		return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Line: line, Props: props, Relations: rels}
	}

	cases := []struct {
		name              string
		baseline, current []facts.Fact
	}{
		{"both empty", nil, nil},
		{"first snapshot", nil, []facts.Fact{sym("A", "a.go", 1, nil)}},
		{"everything removed", []facts.Fact{sym("A", "a.go", 1, nil)}, nil},
		{
			"unchanged",
			[]facts.Fact{sym("A", "a.go", 1, map[string]any{"exported": true})},
			[]facts.Fact{sym("A", "a.go", 1, map[string]any{"exported": true})},
		},
		{
			"props changed",
			[]facts.Fact{sym("A", "a.go", 1, map[string]any{"cyclomatic": 1})},
			[]facts.Fact{sym("A", "a.go", 1, map[string]any{"cyclomatic": 9})},
		},
		{
			// A line shift alone must not count as a change — it is the noise the
			// whole grouping scheme exists to suppress.
			"line shift only",
			[]facts.Fact{sym("A", "a.go", 1, map[string]any{"x": 1})},
			[]facts.Fact{sym("A", "a.go", 40, map[string]any{"x": 1})},
		},
		{
			// Facts sharing a factKey: the case that forced positional pairing.
			"multi-member group, one member dropped",
			[]facts.Fact{sym("A", "a.go", 1, nil), sym("A", "a.go", 9, nil), sym("A", "a.go", 20, nil)},
			[]facts.Fact{sym("A", "a.go", 1, nil), sym("A", "a.go", 9, nil)},
		},
		{
			"multi-member group, one member added",
			[]facts.Fact{sym("A", "a.go", 1, nil)},
			[]facts.Fact{sym("A", "a.go", 1, nil), sym("A", "a.go", 9, nil)},
		},
		{
			"edges added and removed",
			[]facts.Fact{sym("A", "a.go", 1, nil, facts.Relation{Kind: facts.RelCalls, Target: "B"})},
			[]facts.Fact{sym("A", "a.go", 1, nil, facts.Relation{Kind: facts.RelCalls, Target: "C"})},
		},
		{
			// edgeSet is a SET: the same edge twice is one edge.
			"duplicate edges collapse",
			[]facts.Fact{sym("A", "a.go", 1, nil,
				facts.Relation{Kind: facts.RelCalls, Target: "B"},
				facts.Relation{Kind: facts.RelCalls, Target: "B"})},
			[]facts.Fact{sym("A", "a.go", 1, nil)},
		},
		{
			// The discriminator makes two routes on one path distinct facts.
			"routes distinguished by method",
			[]facts.Fact{{Kind: facts.KindRoute, Name: "/u", File: "r.go", Props: map[string]any{"method": "GET"}}},
			[]facts.Fact{
				{Kind: facts.KindRoute, Name: "/u", File: "r.go", Props: map[string]any{"method": "GET"}},
				{Kind: facts.KindRoute, Name: "/u", File: "r.go", Props: map[string]any{"method": "POST"}},
			},
		},
		{
			"kinds tallied separately",
			[]facts.Fact{sym("A", "a.go", 1, nil)},
			[]facts.Fact{
				sym("A", "a.go", 1, nil),
				{Kind: facts.KindModule, Name: "m", File: "m/"},
				{Kind: facts.KindRoute, Name: "/x", File: "r.go", Props: map[string]any{"method": "GET"}},
			},
		},
		{
			"repo separates otherwise identical facts",
			[]facts.Fact{{Kind: facts.KindSymbol, Name: "A", File: "a.go", Repo: "one"}},
			[]facts.Fact{{Kind: facts.KindSymbol, Name: "A", File: "a.go", Repo: "two"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertAgrees(t, tc.baseline, tc.current) })
	}
}

// A fuzz-shaped sweep over randomly mutated snapshots. The hand-written cases above
// cover the shapes I thought of; this covers the ones I did not, and in particular
// hammers multi-member groups — the one place the two implementations CAN diverge,
// and the reason Counts keeps exact intraGroupOrder strings for shared keys. An
// earlier version ordered groups by a hash of that string instead, and this test
// caught it disagreeing with Compute on 11 of 300 rounds.
func TestCounts_MatchesCompute_Random(t *testing.T) {
	rng := rand.New(rand.NewSource(20260813))
	kinds := []string{facts.KindSymbol, facts.KindModule, facts.KindDependency}

	gen := func(n int) []facts.Fact {
		out := make([]facts.Fact, 0, n)
		for i := 0; i < n; i++ {
			// A small name/file space on purpose, so factKey collisions — and
			// therefore multi-member groups — are common rather than incidental.
			f := facts.Fact{
				Kind: kinds[rng.Intn(len(kinds))],
				Name: fmt.Sprintf("N%d", rng.Intn(12)),
				File: fmt.Sprintf("f%d.go", rng.Intn(5)),
				Line: rng.Intn(6),
			}
			if rng.Intn(2) == 0 {
				f.Props = map[string]any{"v": rng.Intn(4)}
			}
			for r := rng.Intn(3); r > 0; r-- {
				f.Relations = append(f.Relations, facts.Relation{
					Kind:   facts.RelCalls,
					Target: fmt.Sprintf("T%d", rng.Intn(8)),
				})
			}
			out = append(out, f)
		}
		return out
	}

	for round := 0; round < 300; round++ {
		baseline, current := gen(rng.Intn(40)), gen(rng.Intn(40))
		t.Run(fmt.Sprintf("round%03d", round), func(t *testing.T) {
			assertAgrees(t, baseline, current)
		})
	}
}

// CountAgainstJSONL is the path the engine uses. It must agree with the in-memory
// form, which the tests above have already graded against Compute.
func TestCountAgainstJSONL_MatchesInMemory(t *testing.T) {
	baseline := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "A", File: "a.go", Line: 1, Props: map[string]any{"x": 1},
			Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "B"}}},
		{Kind: facts.KindModule, Name: "m", File: "m/"},
	}
	current := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "A", File: "a.go", Line: 1, Props: map[string]any{"x": 2}},
		{Kind: facts.KindRoute, Name: "/u", File: "r.go", Props: map[string]any{"method": "GET"}},
	}

	store := facts.NewStore()
	store.Add(baseline...)
	var buf strings.Builder
	if err := store.WriteJSONL(&buf); err != nil {
		t.Fatal(err)
	}
	jsonl := buf.String()

	got, err := CountAgainstJSONL(func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(jsonl)), nil
	}, current)
	if err != nil {
		t.Fatal(err)
	}
	want := CountSnapshots(baseline, current)

	if got.FactsAdded != want.FactsAdded || got.FactsRemoved != want.FactsRemoved ||
		got.FactsChanged != want.FactsChanged || got.EdgesAdded != want.EdgesAdded ||
		got.EdgesRemoved != want.EdgesRemoved {
		t.Errorf("streamed = %+v, in-memory = %+v", got, want)
	}
	assertKindsAgree(t, "added", got.AddedByKind, want.AddedByKind)
	assertKindsAgree(t, "removed", got.RemovedByKind, want.RemovedByKind)
}

// TestCounts_MatchesCompute_Corpus grades the two against each other on a REAL
// snapshot pair. The synthetic cases construct shared-key groups deliberately; this
// one says whether the numbers hold on a graph nobody designed for the test.
//
// Point ENOLA_DIFF_BASELINE and ENOLA_DIFF_CURRENT at two facts.jsonl files:
//
//	ENOLA_DIFF_BASELINE=repo/.enola/previous/facts.jsonl \
//	ENOLA_DIFF_CURRENT=repo/.enola/facts.jsonl go test ./internal/diff/ -run Corpus -v
func TestCounts_MatchesCompute_Corpus(t *testing.T) {
	basePath := os.Getenv("ENOLA_DIFF_BASELINE")
	curPath := os.Getenv("ENOLA_DIFF_CURRENT")
	if basePath == "" || curPath == "" {
		t.Skip("set ENOLA_DIFF_BASELINE and ENOLA_DIFF_CURRENT to two facts.jsonl files")
	}

	load := func(path string) []facts.Fact {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		var out []facts.Fact
		if err := facts.ScanJSONL(f, func(fact facts.Fact) error {
			out = append(out, fact)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	baseline, current := load(basePath), load(curPath)
	t.Logf("baseline %d facts, current %d facts", len(baseline), len(current))
	assertAgrees(t, baseline, current)
}
