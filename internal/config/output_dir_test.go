package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadYAML(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mcp-arch.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

// The ignore glob for enola's own output must be DERIVED from output.dir, not
// assumed to be `.enola/**`. The two agreed only by coincidence, and the coincidence
// ends the moment a user takes up the invitation to configure the directory.
func TestLoad_DerivesIgnoreGlobFromOutputDir(t *testing.T) {
	cfg, err := loadYAML(t, "repo: \".\"\noutput:\n  dir: \".enola-bench\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(cfg.Ignore, ".enola-bench/**") {
		t.Errorf("no ignore glob derived for output.dir; the next snapshot would walk this "+
			"one's artifacts. ignore = %v", cfg.Ignore)
	}
	if !contains(cfg.Ignore, ".enola/**") {
		t.Error("the literal .enola/** was dropped; a repo that used the default before " +
			"switching would start indexing its own history")
	}
}

// Nested directories are ordinary: `x/y/**` matches `x/y` and everything under it.
func TestLoad_DerivesIgnoreGlobForANestedOutputDir(t *testing.T) {
	cfg, err := loadYAML(t, "repo: \".\"\noutput:\n  dir: \"build/enola\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(cfg.Ignore, "build/enola/**") {
		t.Errorf("ignore = %v, want a derived build/enola/** entry", cfg.Ignore)
	}
}

// The default location must not gain a second, identical entry. A duplicate would
// change the ignore-glob hash for every existing user and decline every diff against
// a baseline pinned before the upgrade — a migration cost for no behaviour change.
func TestNormalize_DoesNotDuplicateTheDefaultGlob(t *testing.T) {
	cfg := Default()
	before := len(cfg.Ignore)
	for range 3 {
		if err := cfg.Normalize(); err != nil {
			t.Fatal(err)
		}
	}
	if len(cfg.Ignore) != before {
		t.Errorf("ignore grew from %d to %d entries on a default config", before, len(cfg.Ignore))
	}
}

// Normalize runs from both config.Load and engine.New, so it has to be safe to
// repeat on a config that already went through it.
func TestNormalize_IsIdempotentForACustomDir(t *testing.T) {
	cfg := Default()
	cfg.Output.Dir = "out/enola"
	for range 3 {
		if err := cfg.Normalize(); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, g := range cfg.Ignore {
		if g == "out/enola/**" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("derived glob appears %d times after three Normalize calls, want 1", n)
	}
}

// output.dir is joined to the repository path in half a dozen places, so an absolute
// value silently produced a directory nested INSIDE the repo (/repo/private/tmp/…/out)
// rather than the location asked for. Erroring names the constraint; accepting it
// writes the artifacts somewhere the user did not choose and cannot easily find.
func TestLoad_RejectsAnOutputDirItCannotHonour(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "out")
	for _, tc := range []struct{ name, dir string }{
		{"absolute", abs},
		{"parent", ".."},
		{"escaping", "../elsewhere"},
		{"repo root", "."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadYAML(t, "repo: \".\"\noutput:\n  dir: \""+tc.dir+"\"\n")
			if err == nil {
				t.Fatalf("output.dir %q was accepted", tc.dir)
			}
			if !strings.Contains(err.Error(), "output.dir") {
				t.Errorf("error does not name the setting at fault: %v", err)
			}
		})
	}
}

// A path written with redundant segments must normalize, so the glob and the joined
// directory describe the same place.
func TestLoad_CleansTheOutputDir(t *testing.T) {
	cfg, err := loadYAML(t, "repo: \".\"\noutput:\n  dir: \"./out/./enola\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output.Dir != "out/enola" {
		t.Errorf("Output.Dir = %q, want the cleaned form", cfg.Output.Dir)
	}
	if !contains(cfg.Ignore, "out/enola/**") {
		t.Errorf("ignore = %v, want the glob to match the cleaned directory", cfg.Ignore)
	}
}
