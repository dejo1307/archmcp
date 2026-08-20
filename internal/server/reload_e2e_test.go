package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/engine"
)

// A CLI `--generate` (or `--generate --refresh`) runs in its own process and
// rewrites the artifacts and the workspace receipt under a server that keeps
// serving the graph it loaded at start. This is the sibling-process shape: one
// engine writes, a second engine serves the same workspace, and the served facts
// must follow the disk without a restart.
func TestE2E_ServerReloadsWhenASiblingProcessRewritesTheArtifacts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	ctx := context.Background()

	repo := copyTree(t, filepath.Join("..", "engine", "testdata", "repos", "go_sample"), t.TempDir())

	writer, writerCfg := newTestEngine(t)
	writerCfg.Repo = repo
	generateAndWrite := func() {
		t.Helper()
		if _, err := writer.GenerateSnapshot(ctx, repo, false); err != nil {
			t.Fatalf("writer GenerateSnapshot: %v", err)
		}
		if err := writer.WriteArtifacts(repo); err != nil {
			t.Fatalf("writer WriteArtifacts: %v", err)
		}
		if err := writer.WriteGlobalReceipt(); err != nil {
			t.Fatalf("writer WriteGlobalReceipt: %v", err)
		}
	}
	generateAndWrite()
	gr, err := engine.LoadWorkspaceReceipt(repo)
	if err != nil {
		t.Fatalf("workspace receipt: %v", err)
	}
	firstID := gr.SnapshotID

	served, servedCfg := newTestEngine(t)
	servedCfg.Repo = repo
	s := connect(t, served, servedCfg)

	got := text(s.call(t, "query_facts", map[string]any{"kind": "module"}))
	if !strings.Contains(got, "module") || strings.Contains(got, "Found 0") {
		t.Fatalf("a server started empty over a workspace with artifacts on disk must answer from them:\n%s", got)
	}
	if id := served.Snapshot().Meta.SnapshotID; id != firstID {
		t.Fatalf("served snapshot %q, disk %q", id, firstID)
	}

	if err := os.RemoveAll(filepath.Join(repo, "pkg")); err != nil {
		t.Fatal(err)
	}
	generateAndWrite()
	gr, err = engine.LoadWorkspaceReceipt(repo)
	if err != nil {
		t.Fatalf("workspace receipt: %v", err)
	}
	if gr.SnapshotID == firstID {
		t.Fatalf("the rewrite did not change the snapshot id; the fixture edit did not move the graph")
	}

	s.call(t, "query_facts", map[string]any{"kind": "module"})
	if id := served.Snapshot().Meta.SnapshotID; id != gr.SnapshotID {
		t.Fatalf("after the sibling rewrite the server serves %q, disk holds %q", id, gr.SnapshotID)
	}
}
