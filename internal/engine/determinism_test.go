package engine_test

// Determinism is Enola's core guarantee: snapshotting the same repo twice must
// produce an identical fact graph. This test runs each fixture through two
// independent engines (fresh temp copies, no shared cache) and asserts the
// normalized JSONL output is byte-identical. It complements TestGolden: golden
// catches "the output changed vs. the committed baseline," determinism catches
// "the output is unstable run-to-run" (e.g. map-iteration order leaking through,
// or a concurrency bug under -race).

import (
	"bytes"
	"testing"
)

func TestDeterminism(t *testing.T) {
	for _, f := range fixtures {
		f := f
		t.Run(f.name, func(t *testing.T) {
			first := snapshotFixture(t, f)
			second := snapshotFixture(t, f)
			if !bytes.Equal(first, second) {
				t.Errorf("non-deterministic output for %s:\n%s", f.name, firstDiff(first, second))
			}
		})
	}
}
