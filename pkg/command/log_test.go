package command

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/pkg/facts"
	"github.com/enola-labs/enola/pkg/history"
)

// `enola log /some/other/repo`, run from a directory that has its own mcp-arch.yaml,
// loads that config — the same cwd fallback `check` uses on purpose. The advice printed
// for an empty history must not then tell the user to enable recording in a config
// belonging to a repository they were not asking about: following it would start
// recording a different repo's history, and the reason would be invisible.
func TestConfigToEdit_NamesTheRepoBeingReportedOn(t *testing.T) {
	repo := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "mcp-arch.yaml")

	cfg := config.Default()
	cfg.SourcePath = elsewhere
	if got := configToEdit(repo, cfg); got != filepath.Join(repo, "mcp-arch.yaml") {
		t.Errorf("a config outside the repo must not be the one recommended, got %s", got)
	}

	inside := filepath.Join(repo, "mcp-arch.yaml")
	cfg.SourcePath = inside
	if got := configToEdit(repo, cfg); got != inside {
		t.Errorf("the repo's own config must be recommended as loaded, got %s", got)
	}
}

func onelineEntry(id, at, commit, ref, epoch string, dirty bool, s history.Summary) history.Entry {
	return history.Entry{
		ID:      "sha256:" + id,
		At:      at,
		Epoch:   epoch,
		Git:     &facts.GitInfo{Commit: commit, Ref: ref, Dirty: dirty},
		Summary: s,
	}
}

func TestRenderOneline(t *testing.T) {
	out := renderOneline([]history.Entry{
		onelineEntry("aaa1111", "2026-08-01T10:00:00Z", "c0ffee1111ab", "main", "ep1", false,
			history.Summary{Initial: true, FactCount: 100, InsightCount: 4}),
		onelineEntry("bbb2222", "2026-08-02T10:00:00Z", "deadbeef22cd", "main", "ep1", false,
			history.Summary{FactsAdded: 12, EdgesAdded: 2, FindingsNew: 1}),
	})

	if !strings.Contains(out, "aaa1111") || !strings.Contains(out, "bbb2222") {
		t.Fatalf("both revisions must appear:\n%s", out)
	}
	if !strings.Contains(out, "c0ffee1111ab") {
		t.Errorf("the commit must be shown:\n%s", out)
	}
	// Oldest first: a history read forward is a story; read backward it is a changelog,
	// and "how did it get like this?" is the forward question.
	if strings.Index(out, "aaa1111") > strings.Index(out, "bbb2222") {
		t.Errorf("want oldest first:\n%s", out)
	}
	if strings.Contains(out, "epoch changed") {
		t.Errorf("nothing about enola changed here, so no seam should be drawn:\n%s", out)
	}
}

// A delta computed across an epoch change describes a rebuild, not somebody's work. A
// reader who is not told that will read it as a change to the code — so the seam is drawn
// on its own line rather than left to be inferred from a version column nobody scans.
func TestRenderOneline_MarksAnEpochSeam(t *testing.T) {
	out := renderOneline([]history.Entry{
		onelineEntry("aaa1111", "2026-08-01T10:00:00Z", "c1", "main", "ep1", false, history.Summary{FactsAdded: 1}),
		onelineEntry("bbb2222", "2026-08-02T10:00:00Z", "c2", "main", "ep2", false,
			history.Summary{FactsAdded: 900, FactsRemoved: 850, Incomparable: true}),
	})

	if !strings.Contains(out, "epoch changed") {
		t.Fatalf("want an epoch seam between the two revisions:\n%s", out)
	}
	if !strings.Contains(out, "rebuild noise") {
		t.Errorf("the seam must say what it means, not just that it happened:\n%s", out)
	}
	if !strings.Contains(out, "incomparable") {
		t.Errorf("the affected revision must be marked:\n%s", out)
	}
	// The seam belongs ABOVE the revision that opens the new epoch.
	if strings.Index(out, "epoch changed") > strings.Index(out, "bbb2222") {
		t.Errorf("the seam must precede the revision it explains:\n%s", out)
	}
}

func TestOnlyCommitted(t *testing.T) {
	entries := []history.Entry{
		onelineEntry("aaa1111", "2026-08-01T10:00:00Z", "c1", "main", "ep1", false, history.Summary{}),
		onelineEntry("bbb2222", "2026-08-01T10:05:00Z", "c1", "main", "ep1", true, history.Summary{}),
		{ID: "sha256:ccc3333", At: "2026-08-01T10:06:00Z"}, // no git at all
	}
	got := onlyCommitted(entries)
	if len(got) != 1 || got[0].ID != "sha256:aaa1111" {
		t.Fatalf("want only the committed revision, got %+v", got)
	}
}

func TestDecorations_DirtyAndClean(t *testing.T) {
	clean := decorations(onelineEntry("a", "", "c0ffee1111ab", "main", "ep", false, history.Summary{}))
	if strings.Contains(clean, "dirty") {
		t.Errorf("a clean revision must not be marked dirty: %q", clean)
	}
	dirty := decorations(onelineEntry("a", "", "c0ffee1111ab", "main", "ep", true, history.Summary{}))
	if !strings.Contains(dirty, "dirty") {
		t.Errorf("a working revision must say so: %q", dirty)
	}
}
