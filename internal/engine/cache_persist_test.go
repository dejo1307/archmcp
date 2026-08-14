package engine

import (
	"os"
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
	if err := warm.save(); err != nil {
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

// The point of the flag: no spool is opened and nothing is encoded, on either route
// that would otherwise write — put encodes fresh facts, get carries reused bytes
// forward, and only one of those is obvious.
//
// Encoding for a file nobody will write was the most expensive way to do nothing in
// a snapshot: 800 MB on a kernel-sized repository, reached through a bytes.Buffer
// doubling that cost 1.5 GB, on every `enola --explain`.
func TestExtractorCache_NonPersisting_WritesNothing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cache.json")

	seed := loadExtractorCache(src, true)
	seed.put("reused", []facts.Fact{{Kind: facts.KindSymbol, Name: "A"}})
	if err := seed.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	before := statSize(t, src)

	ro := loadExtractorCache(src, false)
	if _, hit := ro.get("reused"); !hit { // carry-forward route
		t.Fatal("expected a hit")
	}
	ro.put("fresh", []facts.Fact{{Kind: facts.KindSymbol, Name: "B"}}) // encode route
	if err := ro.save(); err != nil {                                  // must be a no-op
		t.Fatalf("save on a non-persisting cache: %v", err)
	}

	if ro.tmp != nil {
		t.Error("a non-persisting cache opened a spool file")
	}
	assertNoStagingFiles(t, dir)
	if after := statSize(t, src); after != before {
		t.Errorf("the existing cache was rewritten (%d -> %d bytes)", before, after)
	}
}

// The persisting cache must be unchanged by the flag's existence: both routes still
// reach the file, or a warm run would silently stop writing its cache back and every
// subsequent run would be cold.
func TestExtractorCache_Persisting_WritesBothRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")

	seed := loadExtractorCache(path, true)
	seed.put("reused", []facts.Fact{{Kind: facts.KindSymbol, Name: "A"}})
	if err := seed.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	c := loadExtractorCache(path, true)
	if _, hit := c.get("reused"); !hit {
		t.Fatal("expected a hit")
	}
	c.put("fresh", []facts.Fact{{Kind: facts.KindSymbol, Name: "B"}})
	if err := c.save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded := loadExtractorCache(path, false)
	for _, key := range []string{"reused", "fresh"} {
		if _, ok := reloaded.prev[key]; !ok {
			t.Errorf("persisting cache did not write %q back", key)
		}
	}
}

func statSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}
