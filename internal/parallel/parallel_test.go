package parallel

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
)

// MapFiles must return results aligned to input order regardless of how the
// concurrent workers are scheduled — this ordering is what makes enola's
// per-file output deterministic.
func TestMapFiles_OrderPreserved(t *testing.T) {
	const n = 500
	items := make([]string, n)
	for i := range items {
		items[i] = strconv.Itoa(i)
	}

	got := MapFiles(context.Background(), items, func(s string) string {
		return s + "!"
	})

	if len(got) != n {
		t.Fatalf("len(got) = %d, want %d", len(got), n)
	}
	for i := range got {
		if want := items[i] + "!"; got[i] != want {
			t.Fatalf("got[%d] = %q, want %q (ordering not preserved)", i, got[i], want)
		}
	}
}

func TestMapFiles_Empty(t *testing.T) {
	got := MapFiles(context.Background(), nil, func(s string) int { return 1 })
	if len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func TestMapFiles_AppliesToEveryItem(t *testing.T) {
	items := []string{"a", "b", "c", "d"}
	var calls atomic.Int32
	MapFiles(context.Background(), items, func(s string) string {
		calls.Add(1)
		return s
	})
	if calls.Load() != int32(len(items)) {
		t.Errorf("fn called %d times, want %d", calls.Load(), len(items))
	}
}

// A cancelled context must not deadlock and must leave not-yet-started slots at
// the zero value. Run under -race to catch any data race on the results slice.
func TestMapFiles_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before starting

	items := make([]string, 100)
	for i := range items {
		items[i] = strconv.Itoa(i)
	}

	got := MapFiles(ctx, items, func(s string) string { return "processed-" + s })
	if len(got) != len(items) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(items))
	}
	// With the context already cancelled, every slot should hold the zero value.
	for i, v := range got {
		if v != "" {
			t.Fatalf("slot %d = %q, expected zero value after cancellation", i, v)
		}
	}
}

func TestMapFiles_Deterministic(t *testing.T) {
	items := make([]string, 200)
	for i := range items {
		items[i] = strconv.Itoa(i * 7)
	}
	fn := func(s string) string { return s + "-x" }

	a := MapFiles(context.Background(), items, fn)
	b := MapFiles(context.Background(), items, fn)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at %d: %q vs %q", i, a[i], b[i])
		}
	}
}
