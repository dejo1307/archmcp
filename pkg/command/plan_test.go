package command

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitPlanPositionals(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "cluster.yaml")
	if err := os.WriteFile(cfg, []byte("repos: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoArg, paths := splitPlanPositionals([]string{"app/models/user.rb", dir, "lib/util.rb"})
	if repoArg != dir {
		t.Errorf("repoArg = %q, want the directory %q", repoArg, dir)
	}
	if len(paths) != 2 || paths[0] != "app/models/user.rb" || paths[1] != "lib/util.rb" {
		t.Errorf("paths = %v", paths)
	}

	repoArg, paths = splitPlanPositionals([]string{cfg, "a.rb"})
	if repoArg != cfg {
		t.Errorf("repoArg = %q, want the config %q", repoArg, cfg)
	}
	if len(paths) != 1 || paths[0] != "a.rb" {
		t.Errorf("paths = %v", paths)
	}

	repoArg, paths = splitPlanPositionals([]string{"a.rb", "b.rb"})
	if repoArg != "" || len(paths) != 2 {
		t.Errorf("repoArg = %q paths = %v, want no repo and two paths", repoArg, paths)
	}

	second := filepath.Join(dir, "second")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	repoArg, paths = splitPlanPositionals([]string{dir, second})
	if repoArg != dir {
		t.Errorf("repoArg = %q, want the first directory to win", repoArg)
	}
	if len(paths) != 1 || paths[0] != second {
		t.Errorf("paths = %v, want the second directory demoted to a path target", paths)
	}
}
