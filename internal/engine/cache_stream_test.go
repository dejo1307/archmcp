package engine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// The cache is loaded and saved by hand-rolled streaming codecs rather than by
// json.Marshal/Unmarshal on cacheFile, so that an 800 MB cache is never held twice.
// These tests hold that streaming to the shape the struct declares — the risk of a
// hand-rolled encoder is a file only its own decoder can read, which would look
// exactly like a permanently cold cache and cost nothing but time, silently.

// TestExtractorCache_EncodeMatchesMarshal is the format guarantee: streaming writes
// byte-for-byte what json.Marshal(cacheFile) wrote before. A cache written by one and
// read by the other must round-trip, because both happen across an upgrade.
func TestExtractorCache_EncodeMatchesMarshal(t *testing.T) {
	entries := map[string]json.RawMessage{
		// Deliberately not in sorted order, and with a key needing JSON escaping.
		"typescript": json.RawMessage(`[{"kind":"symbol","name":"B"}]`),
		"go":         json.RawMessage(`[{"kind":"symbol","name":"A"}]`),
		`odd"key`:    json.RawMessage(`[]`),
	}

	c := &extractorCache{next: entries}
	var streamed bytes.Buffer
	w := bufio.NewWriter(&streamed)
	if err := c.encode(w); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	marshalled, err := json.Marshal(cacheFile{
		Version: cacheVersion,
		Build:   buildIdentity(),
		Entries: entries,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got, want := streamed.String(), string(marshalled); got != want {
		t.Errorf("streamed encoding differs from json.Marshal(cacheFile)\n got: %s\nwant: %s", got, want)
	}
}

// TestExtractorCache_DecodeReadsMarshalledFile — the other direction of the same
// contract: a file produced by json.Marshal (which is what every cache on disk before
// this change is) must still load.
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

	if got := loadExtractorCache(path, true); len(got.prev) != 0 {
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

	if got := loadExtractorCache(path, true); len(got.prev) != 0 {
		t.Errorf("a truncated cache loaded %d entries; it must be discarded whole", len(got.prev))
	}
}

// TestExtractorCache_GetReleasesEntry — get hands the bytes from prev to next rather
// than sharing them. Holding both pinned every reused entry twice for the rest of the
// run, which on a fully-warm kernel snapshot is the entire cache over again.
func TestExtractorCache_GetReleasesEntry(t *testing.T) {
	c := &extractorCache{
		prev: map[string]json.RawMessage{"go": json.RawMessage(`[{"kind":"symbol","name":"A"}]`)},
		next: map[string]json.RawMessage{},
	}

	if _, ok := c.get("go"); !ok {
		t.Fatal("entry should have been found")
	}
	if _, still := c.prev["go"]; still {
		t.Error("prev still holds the entry after get; the bytes are pinned twice")
	}
	if _, carried := c.next["go"]; !carried {
		t.Error("next lost the entry; it would be dropped from the cache on save")
	}
}

// TestExtractorCache_SaveIsAtomic — save stages and renames, so a reader never sees a
// partial file and a failed write leaves the previous cache intact rather than
// truncated. Checked by confirming no staging file survives a successful save.
func TestExtractorCache_SaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "extractor_cache.json")

	c := &extractorCache{prev: map[string]json.RawMessage{}, next: map[string]json.RawMessage{}}
	c.put("go", []facts.Fact{{Kind: facts.KindSymbol, Name: "Fresh"}})
	if err := c.save(path); err != nil {
		t.Fatal(err)
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("staging file %q left behind after a successful save", e.Name())
		}
	}

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
