package engine

import (
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// A cache that will never be saved must still SERVE entries. Read-only modes
// (--explain) reuse a warm cache, and that reuse is most of what makes them fast;
// only the write-back side is pointless.
func TestExtractorCache_NonPersisting_StillServes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")

	// Populate and save through a persisting cache.
	warm := loadExtractorCache(path, true)
	warm.put("key", []facts.Fact{{Kind: facts.KindSymbol, Name: "A"}})
	if err := warm.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	ro := loadExtractorCache(path, false)
	got, hit := ro.get("key")
	if !hit {
		t.Fatal("a non-persisting cache did not serve a stored entry")
	}
	if len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("served the wrong facts: %+v", got)
	}
	if ro.hits != 1 {
		t.Errorf("hits = %d, want 1", ro.hits)
	}
}

// The point of the flag: nothing is accumulated for a write-back that will not
// happen. `next` is where those bytes were held for the whole run — 800 MB on a
// kernel-sized repository, marshalled and then dropped unwritten.
//
// Both routes into `next` are covered, because there are two and only one of them
// is obvious: put() marshals fresh facts, and get() carries reused bytes forward.
func TestExtractorCache_NonPersisting_AccumulatesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")

	seed := loadExtractorCache(path, true)
	seed.put("reused", []facts.Fact{{Kind: facts.KindSymbol, Name: "A"}})
	if err := seed.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	ro := loadExtractorCache(path, false)
	if _, hit := ro.get("reused"); !hit { // carry-forward path
		t.Fatal("expected a hit")
	}
	ro.put("fresh", []facts.Fact{{Kind: facts.KindSymbol, Name: "B"}}) // marshal path

	if len(ro.next) != 0 {
		t.Errorf("non-persisting cache retained %d entries in next, want 0", len(ro.next))
	}
}

// The persisting cache must be unchanged by the flag's introduction: both paths
// still fill `next`, or a warm run would silently stop writing its cache back and
// every subsequent run would be cold.
func TestExtractorCache_Persisting_AccumulatesBothPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")

	seed := loadExtractorCache(path, true)
	seed.put("reused", []facts.Fact{{Kind: facts.KindSymbol, Name: "A"}})
	if err := seed.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	c := loadExtractorCache(path, true)
	if _, hit := c.get("reused"); !hit {
		t.Fatal("expected a hit")
	}
	c.put("fresh", []facts.Fact{{Kind: facts.KindSymbol, Name: "B"}})

	for _, key := range []string{"reused", "fresh"} {
		if _, ok := c.next[key]; !ok {
			t.Errorf("persisting cache did not carry %q forward to next", key)
		}
	}
}
