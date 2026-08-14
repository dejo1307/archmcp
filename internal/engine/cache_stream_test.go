package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// The cache is written and read by hand-rolled streaming codecs rather than by
// json.Marshal/Unmarshal on cacheFile, so that an 800 MB cache is never held in
// memory at all. These tests hold that streaming to the shape the struct declares —
// the risk of a hand-rolled encoder is a file only its own decoder can read, which
// would look exactly like a permanently cold cache and cost nothing but time,
// silently.

// TestExtractorCache_WrittenFileParsesAsCacheFile is the format guarantee: what the
// spool writes is what json.Unmarshal(cacheFile) reads. A cache written by one and
// read by the other must round-trip, because both happen across an upgrade.
//
// It compares the PARSED value, not the bytes. Entries are written in production
// order now rather than sorted, so the file is no longer byte-identical to
// json.Marshal's output — see save. What has to hold is that both sides agree on
// the contents.
func TestExtractorCache_WrittenFileParsesAsCacheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extractor_cache.json")
	c := loadExtractorCache(path, true)

	// Deliberately written in non-sorted order, with a key needing JSON escaping.
	c.put("typescript", []facts.Fact{{Kind: facts.KindSymbol, Name: "B"}})
	c.put("go", []facts.Fact{{Kind: facts.KindSymbol, Name: "A"}})
	c.put(`odd"key`, nil)
	if err := c.save(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var parsed cacheFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("the file the spool wrote is not valid cacheFile JSON: %v\n%s", err, data)
	}
	if parsed.Version != cacheVersion {
		t.Errorf("version = %q, want %q", parsed.Version, cacheVersion)
	}
	if parsed.Build != buildIdentity() {
		t.Errorf("build = %q, want %q", parsed.Build, buildIdentity())
	}
	want := map[string]string{
		"typescript": `[{"kind":"symbol","name":"B"}]`,
		"go":         `[{"kind":"symbol","name":"A"}]`,
		`odd"key`:    `[]`,
	}
	if len(parsed.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(parsed.Entries), len(want), parsed.Entries)
	}
	for k, v := range want {
		if got := string(parsed.Entries[k]); got != v {
			t.Errorf("entry %q = %s, want %s", k, got, v)
		}
	}
}

// TestExtractorCache_DecodeReadsMarshalledFile — the other direction of the same
// contract: a file produced by json.Marshal (which is what every cache on disk before
// the streaming writer is) must still load.
func TestExtractorCache_DecodeReadsMarshalledFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extractor_cache.json")
	writeCacheFile(t, path, cacheFile{
		Version: cacheVersion,
		Build:   buildIdentity(),
		Entries: map[string]json.RawMessage{
			"go":     json.RawMessage(`[{"kind":"symbol","name":"A"}]`),
			"python": json.RawMessage(`[{"kind":"symbol","name":"B"}]`),
		},
	})

	c := loadExtractorCache(path, true)
	defer c.discard()
	if len(c.prev) != 2 {
		t.Fatalf("loaded %d entries from a json.Marshal-written cache, want 2", len(c.prev))
	}
	ff, ok := c.get("go")
	if !ok || len(ff) != 1 || ff[0].Name != "A" {
		t.Errorf("entry did not survive the round trip: ok=%v facts=%+v", ok, ff)
	}
}

// TestExtractorCache_StaleVersionSkipsEntries — the point of checking version and
// build as they stream past is to abort before touching entries. Asserted through the
// observable consequence: a stale file loads nothing, however large its entries are.
func TestExtractorCache_StaleVersionSkipsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extractor_cache.json")
	writeCacheFile(t, path, cacheFile{
		Version: cacheVersion + "-not-this-one",
		Build:   buildIdentity(),
		Entries: map[string]json.RawMessage{"go": json.RawMessage(`[{"kind":"symbol","name":"Stale"}]`)},
	})

	got := loadExtractorCache(path, true)
	defer got.discard()
	if len(got.prev) != 0 {
		t.Errorf("a cache with a foreign version was loaded (%d entries)", len(got.prev))
	}
}

// TestExtractorCache_TruncatedFileIsCold — a save killed mid-write, or any other
// corruption, must yield a COMPLETELY cold cache. A partially populated one would
// serve some extractors from a half-written file and re-parse the rest, which is the
// one outcome worse than re-parsing everything.
func TestExtractorCache_TruncatedFileIsCold(t *testing.T) {
	full, err := json.Marshal(cacheFile{
		Version: cacheVersion,
		Build:   buildIdentity(),
		Entries: map[string]json.RawMessage{
			"go":     json.RawMessage(`[{"kind":"symbol","name":"A"}]`),
			"python": json.RawMessage(`[{"kind":"symbol","name":"B"}]`),
			"ruby":   json.RawMessage(`[{"kind":"symbol","name":"C"}]`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "extractor_cache.json")
	// Cut inside the entries object, after at least one entry has been written.
	if err := os.WriteFile(path, full[:len(full)-20], 0o644); err != nil {
		t.Fatal(err)
	}

	got := loadExtractorCache(path, true)
	defer got.discard()
	if len(got.prev) != 0 {
		t.Errorf("a truncated cache loaded %d entries; it must be discarded whole", len(got.prev))
	}
}

// TestExtractorCache_GetReleasesEntry — get writes the reused bytes straight through
// to the spool and drops its reference. Keeping them (as a next map did) pinned every
// reused entry for the rest of the run, alongside the facts just decoded from them.
func TestExtractorCache_GetReleasesEntry(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "extractor_cache.json")
	writeCacheFile(t, src, cacheFile{
		Version: cacheVersion,
		Build:   buildIdentity(),
		Entries: map[string]json.RawMessage{"go": json.RawMessage(`[{"kind":"symbol","name":"A"}]`)},
	})

	c := loadExtractorCache(src, true)
	if _, ok := c.get("go"); !ok {
		t.Fatal("entry should have been found")
	}
	if _, still := c.prev["go"]; still {
		t.Error("prev still holds the entry after get; the bytes are pinned for the rest of the run")
	}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}

	// Released, but not lost: it has to reach the new file, or every warm run would
	// quietly shrink the cache to the entries it re-parsed.
	reloaded := loadExtractorCache(src, false)
	if _, ok := reloaded.get("go"); !ok {
		t.Error("the carried-forward entry did not reach the saved cache")
	}
}

// TestExtractorCache_WritesThroughBeforeSave is the property this design exists for:
// entry bytes are on disk while the run is still going, not accumulated until save.
//
// Asserted with an entry comfortably larger than the spool's buffer, so bufio must
// have flushed it — a small entry proves nothing, since it would legitimately still
// be sitting in the buffer.
func TestExtractorCache_WritesThroughBeforeSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extractor_cache.json")
	c := loadExtractorCache(path, true)
	defer c.discard()

	big := make([]facts.Fact, 40_000) // a few MB of JSON, well over cacheBufSize
	for i := range big {
		big[i] = facts.Fact{Kind: facts.KindSymbol, Name: strings.Repeat("n", 64), File: "a/b/c.go"}
	}
	c.put("go", big)

	fi, err := os.Stat(c.tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < cacheBufSize {
		t.Errorf("only %d bytes reached the spool before save; entries are being buffered in memory", fi.Size())
	}
}

// TestExtractorCache_SaveIsAtomic — save stages and renames, so a reader never sees a
// partial file and a failed write leaves the previous cache intact rather than
// truncated. Checked by confirming no staging file survives a successful save.
func TestExtractorCache_SaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "extractor_cache.json")

	c := loadExtractorCache(path, true)
	c.put("go", []facts.Fact{{Kind: facts.KindSymbol, Name: "Fresh"}})
	if err := c.save(); err != nil {
		t.Fatal(err)
	}

	assertNoStagingFiles(t, dir)

	// The mode must match what os.WriteFile produced; CreateTemp defaults to 0600,
	// which would make the cache unreadable by another user on a shared checkout.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Errorf("cache mode is %v, want 0644", got)
	}
}

// TestExtractorCache_DiscardLeavesNothing — the spool is open from the moment the
// cache is created, so a run that never reaches save (a failed extraction) must not
// leave it behind. Every snapshot defers discard for exactly this.
func TestExtractorCache_DiscardLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "extractor_cache.json")

	c := loadExtractorCache(path, true)
	c.put("go", []facts.Fact{{Kind: facts.KindSymbol, Name: "Abandoned"}})
	c.discard()

	assertNoStagingFiles(t, dir)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("discard published the cache anyway (stat err = %v)", err)
	}
	// Deferred everywhere, so it has to tolerate following a successful save too.
	c.discard()
}

// TestExtractorCache_UnreferencedKeyIsDropped — the cache garbage-collects itself:
// only keys touched this run are written back, so an extractor that stops running (a
// language removed from the repository) does not keep its facts alive forever.
func TestExtractorCache_UnreferencedKeyIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "extractor_cache.json")
	writeCacheFile(t, path, cacheFile{
		Version: cacheVersion,
		Build:   buildIdentity(),
		Entries: map[string]json.RawMessage{
			"kept":    json.RawMessage(`[{"kind":"symbol","name":"A"}]`),
			"dropped": json.RawMessage(`[{"kind":"symbol","name":"B"}]`),
		},
	})

	c := loadExtractorCache(path, true)
	if _, ok := c.get("kept"); !ok { // "dropped" is deliberately never referenced
		t.Fatal("expected a hit for the referenced key")
	}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}

	reloaded := loadExtractorCache(path, false)
	if _, ok := reloaded.prev["kept"]; !ok {
		t.Error("the referenced key did not survive the save")
	}
	if _, ok := reloaded.prev["dropped"]; ok {
		t.Error("an unreferenced key survived; stale entries would accumulate forever")
	}
}

// assertNoStagingFiles fails if any spool temp file is left in dir.
func assertNoStagingFiles(t *testing.T, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("staging file %q left behind", e.Name())
		}
	}
}
