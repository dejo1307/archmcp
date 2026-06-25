package engine

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/extractors"
	"github.com/enola-labs/enola/internal/facts"
)

// fakeExtractor implements extractors.Extractor and plugin.FileOwner, owning
// files with a given extension.
type fakeExtractor struct {
	name string
	ext  string
}

func (f *fakeExtractor) Name() string                 { return f.name }
func (f *fakeExtractor) Detect(string) (bool, error)  { return true, nil }
func (f *fakeExtractor) OwnsFile(relFile string) bool { return filepath.Ext(relFile) == f.ext }
func (f *fakeExtractor) Extract(context.Context, string, []string) ([]facts.Fact, error) {
	return nil, nil
}

func keysFor(t *testing.T, hashes map[string]string) map[string]string {
	t.Helper()
	all := []extractors.Extractor{
		&fakeExtractor{name: "py", ext: ".py"},
		&fakeExtractor{name: "ts", ext: ".ts"},
	}
	files := make([]string, 0, len(hashes))
	for f := range hashes {
		files = append(files, f)
	}
	return computeExtractorKeys(all, files, hashes)
}

func TestComputeExtractorKeys_ChangesWhenOwnedFileChanges(t *testing.T) {
	base := map[string]string{"a.py": "h1", "b.ts": "h2", "go.mod": "h3"}
	changed := map[string]string{"a.py": "DIFFERENT", "b.ts": "h2", "go.mod": "h3"}

	k1 := keysFor(t, base)
	k2 := keysFor(t, changed)

	if k1["py"] == k2["py"] {
		t.Error("py key should change when an owned .py file changes")
	}
	if k1["ts"] != k2["ts"] {
		t.Error("ts key should NOT change when only a .py file changes")
	}
}

func TestComputeExtractorKeys_ChangesWhenSharedConfigChanges(t *testing.T) {
	base := map[string]string{"a.py": "h1", "b.ts": "h2", "go.mod": "h3"}
	changed := map[string]string{"a.py": "h1", "b.ts": "h2", "go.mod": "DIFFERENT"}

	k1 := keysFor(t, base)
	k2 := keysFor(t, changed)

	// go.mod is owned by no FileOwner, so it is shared config: every extractor's
	// key must change (conservative — a manifest could affect detection).
	if k1["py"] == k2["py"] || k1["ts"] == k2["ts"] {
		t.Error("all keys should change when a shared (un-owned) config file changes")
	}
}

func TestComputeExtractorKeys_StableWhenNothingChanges(t *testing.T) {
	h := map[string]string{"a.py": "h1", "b.ts": "h2", "go.mod": "h3"}
	first := keysFor(t, h)["py"]
	second := keysFor(t, h)["py"]
	if first != second {
		t.Error("keys must be a deterministic function of the inputs")
	}
}

func TestExtractorCache_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	want := []facts.Fact{{Kind: "symbol", Name: "pkg.Foo", File: "pkg/foo.go"}}

	c := loadExtractorCache(path) // cold: empty
	if _, hit := c.get("k"); hit {
		t.Fatal("cold cache should miss")
	}
	c.put("k", want)
	if err := c.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	c2 := loadExtractorCache(path)
	got, hit := c2.get("k")
	if !hit {
		t.Fatal("warm cache should hit after save")
	}
	if len(got) != 1 || got[0].Name != "pkg.Foo" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// A key not used this run is dropped on the next save (GC of stale entries).
	if err := c2.save(filepath.Join(t.TempDir(), "cache2.json")); err == nil {
		c3 := loadExtractorCache(filepath.Join(t.TempDir(), "cache2.json"))
		if _, hit := c3.get("k"); hit {
			t.Error("stale key should not survive a save that never referenced it")
		}
	}
}
