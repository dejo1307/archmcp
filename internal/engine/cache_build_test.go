package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// writeCacheFile lays down a cache file with an explicit version/build stamp.
func writeCacheFile(t *testing.T, path string, cf cacheFile) {
	t.Helper()
	data, err := json.Marshal(cf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExtractorCache_RejectsAnotherBuildsEntries is the guarantee that a snapshot's
// facts depend on the tree and the binary, never on which binary happened to write the
// cache last.
//
// cacheVersion is a constant a human has to remember to bump. When an extractor changes
// and the bump is missed, every entry the old binary wrote keeps being served by the new
// one for files whose contents never changed — silently. Observed in practice: a
// snapshot 568 facts larger than a cold run of the same binary on the same tree, which
// then surfaced as hundreds of phantom "removed" facts in the next diff.
func TestExtractorCache_RejectsAnotherBuildsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extractor_cache.json")
	entries := map[string]json.RawMessage{"go": json.RawMessage(`[{"kind":"symbol","name":"Stale"}]`)}

	writeCacheFile(t, path, cacheFile{
		Version: cacheVersion,
		Build:   "some-other-build|123|456",
		Entries: entries,
	})

	if got := loadExtractorCache(path); len(got.prev) != 0 {
		t.Errorf("a cache written by a different build was loaded (%d entries); it must be discarded", len(got.prev))
	}
}

// TestExtractorCache_RejectsPreBuildStampEntries — a cache written before the build
// stamp existed has no way to say which binary produced it, so it cannot be trusted.
func TestExtractorCache_RejectsPreBuildStampEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extractor_cache.json")
	// The exact on-disk shape of the previous format: version + entries, no build.
	legacy := `{"version":"` + cacheVersion + `","entries":{"go":[{"kind":"symbol","name":"Stale"}]}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := loadExtractorCache(path); len(got.prev) != 0 {
		t.Errorf("a cache predating the build stamp was loaded (%d entries); it must be discarded", len(got.prev))
	}
}

// TestExtractorCache_ReusesOwnEntries — the invalidation must not be so eager that the
// cache never hits, or every snapshot silently becomes a cold parse and the incremental
// story is gone.
func TestExtractorCache_ReusesOwnEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extractor_cache.json")

	c := &extractorCache{prev: map[string]json.RawMessage{}, next: map[string]json.RawMessage{}}
	c.put("go", []facts.Fact{{Kind: facts.KindSymbol, Name: "Fresh"}})
	if err := c.save(path); err != nil {
		t.Fatal(err)
	}

	got := loadExtractorCache(path)
	if len(got.prev) != 1 {
		t.Fatalf("a cache written by this same binary was not reused: %d entries", len(got.prev))
	}
	if _, ok := got.get("go"); !ok {
		t.Error("entry written by this binary could not be read back")
	}
}

// TestBuildIdentity_IsStableWithinAProcess — it is consulted on both save and load, so
// an unstable value would mean a cache that can never be reused.
func TestBuildIdentity_IsStableWithinAProcess(t *testing.T) {
	first := buildIdentity()
	if first == "" {
		t.Fatal("buildIdentity is empty; every cache would be indistinguishable")
	}
	for i := 0; i < 3; i++ {
		if got := buildIdentity(); got != first {
			t.Fatalf("buildIdentity is unstable: %q then %q", first, got)
		}
	}
}
