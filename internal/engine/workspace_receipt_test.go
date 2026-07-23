package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/facts"
)

// newWorkspaceEngine builds an engine pinned to repoPath holding one fact, so
// each test workspace produces a distinguishable graph.
func newWorkspaceEngine(t *testing.T, repoPath, label string) *Engine {
	t.Helper()
	cfg := config.Default()
	cfg.Repo = repoPath
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	eng.store.Add(facts.Fact{Kind: facts.KindModule, Name: "m-" + label, Repo: label})
	eng.SetSnapshot(&facts.Snapshot{Meta: facts.SnapshotMeta{
		RepoPath: repoPath, SnapshotID: "sha256:" + label, FactCount: 1,
	}})
	return eng
}

// TestWorkspaceReceiptsAreIsolated is the regression test for cross-terminal
// graph bleed: a user commonly runs one enola server per agent terminal, and
// ~/.enola/receipt.json can only describe one of them. Each workspace must
// therefore also get its own receipt, so a restart in repo A restores A's graph
// rather than whatever a sibling terminal snapshotted last.
func TestWorkspaceReceiptsAreIsolated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repoA := filepath.Join(t.TempDir(), "alpha")
	repoB := filepath.Join(t.TempDir(), "beta")

	engA := newWorkspaceEngine(t, repoA, "alpha")
	if err := engA.WriteGlobalReceipt(); err != nil {
		t.Fatalf("WriteGlobalReceipt(alpha): %v", err)
	}
	// The second workspace writes afterwards, so the machine-wide receipt now
	// describes beta — exactly the situation that used to poison alpha's restart.
	engB := newWorkspaceEngine(t, repoB, "beta")
	if err := engB.WriteGlobalReceipt(); err != nil {
		t.Fatalf("WriteGlobalReceipt(beta): %v", err)
	}

	shared, err := LoadGlobalReceipt()
	if err != nil {
		t.Fatalf("LoadGlobalReceipt: %v", err)
	}
	if shared.SnapshotID != "sha256:beta" {
		t.Fatalf("machine-wide receipt = %q, want the last writer (beta)", shared.SnapshotID)
	}

	// Each workspace's own receipt must still describe that workspace.
	for _, tc := range []struct{ repo, want string }{{repoA, "sha256:alpha"}, {repoB, "sha256:beta"}} {
		got, err := LoadWorkspaceReceipt(tc.repo)
		if err != nil {
			t.Fatalf("LoadWorkspaceReceipt(%s): %v", tc.repo, err)
		}
		if got.SnapshotID != tc.want {
			t.Errorf("workspace receipt for %s = %q, want %q — a sibling server's graph leaked in",
				tc.repo, got.SnapshotID, tc.want)
		}
	}
}

// TestWorkspaceKeyDisambiguatesSameBaseName guards the keying: two repos sharing
// a directory name (a monorepo checked out twice, say) must not share a receipt.
func TestWorkspaceKeyDisambiguatesSameBaseName(t *testing.T) {
	a := workspaceKey("/one/api")
	b := workspaceKey("/two/api")
	if a == b {
		t.Fatalf("same key %q for /one/api and /two/api", a)
	}
	for _, k := range []string{a, b} {
		if filepath.Base(k) != k {
			t.Errorf("key %q is not a plain filename stem", k)
		}
	}
}

// TestGraphReceiptReadsLiveEngine covers what the dashboard renders: the graph
// receipt assembled in memory, which must reflect this engine even when the
// shared file on disk says something else.
func TestGraphReceiptReadsLiveEngine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	repo := filepath.Join(t.TempDir(), "gamma")
	eng := newWorkspaceEngine(t, repo, "gamma")

	// Plant a conflicting machine-wide receipt, as another running server would.
	if err := os.MkdirAll(filepath.Join(home, ".enola"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".enola", "receipt.json"),
		[]byte(`{"snapshot_id":"sha256:someone-else"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	gr := eng.GraphReceipt()
	if gr == nil {
		t.Fatal("GraphReceipt() = nil, want the live graph")
	}
	if gr.SnapshotID != "sha256:gamma" {
		t.Errorf("SnapshotID = %q, want the live engine's sha256:gamma", gr.SnapshotID)
	}
	if len(gr.Repos) != 1 || gr.Repos[0].Label != "gamma" {
		t.Errorf("Repos = %+v, want just gamma", gr.Repos)
	}
}

// TestGraphReceiptWithoutSnapshot returns nil rather than an empty shell, which
// is what makes the dashboard show its "no graph loaded" note.
func TestGraphReceiptWithoutSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	eng, err := New(config.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if gr := eng.GraphReceipt(); gr != nil {
		t.Errorf("GraphReceipt() = %+v, want nil with no snapshot loaded", gr)
	}
}
