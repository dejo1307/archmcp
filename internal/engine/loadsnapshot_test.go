package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/facts"
)

// LoadSnapshotDir returns the facts of a store it creates, loads and drops — so it
// hands out a reference rather than a copy. These pin that the reference is sound for
// what callers actually do with it: read it (the history's delta summary, diff_snapshot,
// enola check) and feed it into another store (baseStore.Add).

func writeSnapshotDir(t *testing.T, dir string, lines ...string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "facts.jsonl"), joinLines(lines))
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func TestLoadSnapshotDir_ReturnsUsableFacts(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotDir(t, dir,
		`{"kind":"symbol","name":"A","file":"a.go","line":1,"props":{"symbol_kind":"function"},"relations":[{"kind":"calls","target":"B"}]}`,
		`{"kind":"symbol","name":"B","file":"b.go","line":2}`,
	)

	snap, err := engine.LoadSnapshotDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Facts) != 2 {
		t.Fatalf("loaded %d facts, want 2", len(snap.Facts))
	}
	byName := map[string]facts.Fact{}
	for _, f := range snap.Facts {
		byName[f.Name] = f
	}
	a := byName["A"]
	if a.File != "a.go" || a.Line != 1 || a.PropString("symbol_kind") != "function" {
		t.Errorf("fact A did not survive the load: %+v", a)
	}
	if len(a.Relations) != 1 || a.Relations[0].Target != "B" {
		t.Errorf("fact A lost its relation: %+v", a.Relations)
	}
}

// TestLoadSnapshotDir_FactsSurviveBeingAddedToAnotherStore is the pattern the diff paths
// use: load a baseline, then pour it into a store to compare against. Store.Add takes
// ownership of what it is given and rewrites relation targets in place while interning
// them, so this is the one caller that writes through the returned reference. It must
// stay a value-preserving rewrite.
func TestLoadSnapshotDir_FactsSurviveBeingAddedToAnotherStore(t *testing.T) {
	dir := t.TempDir()
	writeSnapshotDir(t, dir,
		`{"kind":"symbol","name":"A","file":"a.go","relations":[{"kind":"calls","target":"B"},{"kind":"calls","target":"C"}]}`,
		`{"kind":"symbol","name":"B","file":"b.go","relations":[{"kind":"calls","target":"C"}]}`,
	)

	snap, err := engine.LoadSnapshotDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	store := facts.NewStore()
	store.Add(snap.Facts...)

	if store.Count() != 2 {
		t.Fatalf("store holds %d facts, want 2", store.Count())
	}
	// The snapshot's own view must still read correctly after Add rewrote through it.
	for _, f := range snap.Facts {
		for _, r := range f.Relations {
			if r.Target != "B" && r.Target != "C" {
				t.Errorf("fact %q relation target corrupted to %q", f.Name, r.Target)
			}
		}
	}
	got := store.ByName("A")
	if len(got) != 1 || len(got[0].Relations) != 2 {
		t.Fatalf("fact A did not land intact: %+v", got)
	}
}
