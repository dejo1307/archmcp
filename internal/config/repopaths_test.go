package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepoPaths_SingleRepoUnchanged pins the historical behaviour: with no `repos:`
// the single `repo:` resolves against the WORKING directory, and callers get exactly
// one path so they need no special case.
func TestRepoPaths_SingleRepoUnchanged(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := (&Config{Repo: "."}).RepoPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != cwd {
		t.Errorf("RepoPaths() = %v, want [%s]", got, cwd)
	}
}

// TestRepoPaths_ResolvedAgainstConfigDir is the rule that makes a cluster config
// portable: entries are relative to the config FILE, not to wherever the command
// happened to be run from. A checked-in cluster file must mean the same thing on a
// developer's machine and in CI, whose working directories differ.
func TestRepoPaths_ResolvedAgainstConfigDir(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "ci", "cluster.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Repo:       ".",
		Repos:      []string{"../backend", "../frontend"},
		SourcePath: cfgPath,
	}
	got, err := cfg.RepoPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "backend"), filepath.Join(root, "frontend")}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("RepoPaths() = %v, want %v", got, want)
	}
}

// TestRepoPaths_OrderIsPreserved matters because order is semantic: the first repo
// resets the store and the rest append, so a reordered list is a different graph.
func TestRepoPaths_OrderIsPreserved(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{
		Repos:      []string{"c", "a", "b"},
		SourcePath: filepath.Join(root, "cluster.yaml"),
	}
	got, err := cfg.RepoPaths()
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"c", "a", "b"} {
		if filepath.Base(got[i]) != want {
			t.Errorf("position %d = %s, want %s (order is semantic)", i, filepath.Base(got[i]), want)
		}
	}
}

// TestRepoPaths_DuplicatesDropped guards a silent corruption: a repository listed
// twice would be indexed twice, the second pass appending a duplicate of every fact
// the first contributed.
func TestRepoPaths_DuplicatesDropped(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{
		Repos:      []string{"api", "./api", "web", filepath.Join(root, "api")},
		SourcePath: filepath.Join(root, "cluster.yaml"),
	}
	got, err := cfg.RepoPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("RepoPaths() = %v, want 2 entries (api, web)", got)
	}
	if filepath.Base(got[0]) != "api" || filepath.Base(got[1]) != "web" {
		t.Errorf("RepoPaths() = %v, want [api web]", got)
	}
}

// TestRepoPaths_AbsoluteEntriesUntouched — an absolute entry is already unambiguous
// and must not be joined onto the config directory.
func TestRepoPaths_AbsoluteEntriesUntouched(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "elsewhere", "svc")
	cfg := &Config{Repos: []string{abs}, SourcePath: filepath.Join(root, "ci", "cluster.yaml")}
	got, err := cfg.RepoPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != abs {
		t.Errorf("RepoPaths() = %v, want [%s]", got, abs)
	}
}

// TestRepoPaths_EmptyReposIsAnError — `repos:` present but unusable is a config
// mistake, and failing loudly beats silently indexing the cwd as if it were the
// cluster.
func TestRepoPaths_EmptyReposIsAnError(t *testing.T) {
	if _, err := (&Config{Repo: ".", Repos: []string{"", "  "}}).RepoPaths(); err == nil {
		t.Error("want an error when repos: is set but yields no paths")
	}
}

// TestLoad_RecordsSourcePath — RepoPaths' config-relative rule depends on Load
// recording where the file came from.
func TestLoad_RecordsSourcePath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(p, []byte("repos:\n  - ../a\n  - ../b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourcePath != p {
		t.Errorf("SourcePath = %q, want %q", cfg.SourcePath, p)
	}
	got, err := cfg.RepoPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(dir), filepath.Base(dir))
	_ = want
	if len(got) != 2 || filepath.Base(got[0]) != "a" || filepath.Base(got[1]) != "b" {
		t.Errorf("RepoPaths() = %v, want [.../a .../b] resolved beside the config", got)
	}
}
