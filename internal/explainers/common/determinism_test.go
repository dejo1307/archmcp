package common

import (
	"math/rand"
	"testing"
)

// MeanStdDev must be a function of the MULTISET, bit for bit — not of the order the
// caller happened to iterate in.
//
// Float addition is not associative, so a sequential reduction over the same numbers
// in a different order differs in the last bits. Callers build these slices by
// walking the fact store, whose order reflects concurrent extraction and changes
// between runs, and god-class puts the resulting threshold straight into its output:
// across 90 runs of 30 repositories, facts.jsonl was byte-identical every time while
// insights.json moved on one repository, 11 of 25 findings, spread ~1.9e-15.
//
// Bitwise equality is the right assertion. An epsilon comparison would pass on the
// broken code, since the whole defect is a difference far below any tolerance
// anybody would choose — and far above zero, which is what byte-comparing an
// artifact requires.
func TestMeanStdDev_DependsOnlyOnTheMultiset(t *testing.T) {
	// Values that actually exercise the associativity problem: mixing magnitudes is
	// what makes the summation order visible. A uniform slice would pass either way.
	base := []float64{1, 1, 2, 3, 5, 8, 13, 21, 34, 55, 89, 144, 1e6, 1e-6, 7, 7, 7, 0.1, 0.2, 0.3}
	wantMean, wantStd := MeanStdDev(base)

	r := rand.New(rand.NewSource(1))
	for i := range 200 {
		shuffled := append([]float64(nil), base...)
		r.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		mean, std := MeanStdDev(shuffled)
		if mean != wantMean || std != wantStd {
			t.Fatalf("shuffle %d changed the result:\n mean %v -> %v\n  std %v -> %v\n order %v",
				i, wantMean, mean, wantStd, std, shuffled)
		}
	}
}

// The same guarantee, stated where the callers actually consume it.
func TestOutlierThreshold_DependsOnlyOnTheMultiset(t *testing.T) {
	base := []float64{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8, 9, 7, 9, 3, 1e5, 1e-5}
	want := OutlierThreshold(base, 2)

	r := rand.New(rand.NewSource(2))
	for i := range 200 {
		shuffled := append([]float64(nil), base...)
		r.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if got := OutlierThreshold(shuffled, 2); got != want {
			t.Fatalf("shuffle %d: threshold %v -> %v", i, want, got)
		}
	}
}

// Sorting a copy rather than in place. Callers pass slices they keep using —
// god-class builds `values` alongside a fanIn map it indexes afterwards — so
// reordering one under them would be a silent action at a distance.
func TestMeanStdDev_DoesNotReorderTheCallersSlice(t *testing.T) {
	values := []float64{5, 3, 9, 1}
	before := append([]float64(nil), values...)

	MeanStdDev(values)

	for i := range values {
		if values[i] != before[i] {
			t.Fatalf("the caller's slice was reordered: %v -> %v", before, values)
		}
	}
}
