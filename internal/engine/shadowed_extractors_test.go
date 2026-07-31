package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/config"
)

// snapshotWith runs one snapshot over a repo holding a single .py file, with the
// given extractor list. Two extractors are registered and both detect, so which
// one is missing from the list is the only variable.
func snapshotWith(t *testing.T, extractors []string, explicit bool) []string {
	t.Helper()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "a.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Repo = repo
	cfg.Extractors = extractors
	cfg.ExtractorsExplicit = explicit
	cfg.Explainers = nil
	cfg.Renderers = nil

	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.SetPersistCache(false)
	e.RegisterExtractor(&fakeExtractor{name: "py", ext: ".py"})
	e.RegisterExtractor(&fakeExtractor{name: "ts", ext: ".ts"})

	snap, err := e.GenerateSnapshot(context.Background(), repo, false)
	if err != nil {
		t.Fatalf("GenerateSnapshot: %v", err)
	}
	return snap.Meta.ShadowedExtractors
}

// A hand-written `extractors:` list REPLACES the built-in one, so an extractor that
// exists and applies to the repository can be excluded by a config that predates
// it. The failure is invisible from the outside — a disabled extractor is never
// tried, so it appears nowhere in the log, and the repository just looks empty.
// This is the record that names it.
func TestSnapshot_RecordsExtractorsShadowedByAnExplicitList(t *testing.T) {
	got := snapshotWith(t, []string{"py"}, true)
	if strings.Join(got, ",") != "ts" {
		t.Errorf("ShadowedExtractors = %v, want [ts] — the registered extractor that "+
			"detected this repo but was excluded by the config", got)
	}
}

// Inheriting the defaults is not a choice about any particular extractor, so a
// disabled-by-default extractor is not "shadowed" — reporting it would make the
// warning fire on every run and teach the reader to ignore it.
func TestSnapshot_NoShadowReportWhenTheListWasInherited(t *testing.T) {
	if got := snapshotWith(t, []string{"py"}, false); len(got) != 0 {
		t.Errorf("ShadowedExtractors = %v, want none when the list came from the defaults", got)
	}
}

// Nothing excluded, nothing to report — the common case must stay silent.
func TestSnapshot_NoShadowReportWhenEverythingIsEnabled(t *testing.T) {
	if got := snapshotWith(t, []string{"py", "ts"}, true); len(got) != 0 {
		t.Errorf("ShadowedExtractors = %v, want none when the config enables both", got)
	}
}
