package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/bootstrap"
)

// The config governs which extractors run and which paths are ignored, so loading
// the wrong one does not fail — it analyses something other than what was asked
// for. These tests cover the two halves of that: that the resolved path is always
// reported, and that a config beside an INSTALLED binary is refused.

// writeConfig puts a config at path whose repo value is a marker, so a test can
// tell which of several candidate files was actually loaded.
func writeConfig(t *testing.T, path, marker string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("repo: \""+marker+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// exeDir is the directory holding the running test binary — the directory
// ResolveConfig treats as the "bundled" location. Writing there is how the
// executable-adjacent fallback can be exercised at all.
func exeDir(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	if resolved, rErr := filepath.EvalSymlinks(exe); rErr == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

// bundleConfig writes a config next to the test binary and removes it afterwards,
// so it cannot leak into any other test in this package.
func bundleConfig(t *testing.T, marker string) {
	t.Helper()
	p := filepath.Join(exeDir(t), "mcp-arch.yaml")
	if _, err := os.Stat(p); err == nil {
		t.Skipf("a config already exists at %s", p)
	}
	writeConfig(t, p, marker)
	t.Cleanup(func() {
		if err := os.Remove(p); err != nil {
			t.Errorf("removing %s: %v — it would leak into the other tests in this package", p, err)
		}
	})
}

func TestResolveConfig_WorkingDirectoryConfigWins(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, filepath.Join(dir, "mcp-arch.yaml"), "/from-cwd")
	t.Chdir(dir)

	cfg, note, err := bootstrap.ResolveConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repo != "/from-cwd" {
		t.Errorf("Repo = %q, want the cwd config's value", cfg.Repo)
	}
	if !strings.Contains(note, filepath.Join(dir, "mcp-arch.yaml")) {
		t.Errorf("note = %q, want it to name the resolved config path", note)
	}
}

// The note must say "defaults" rather than naming a file that was only looked for:
// a note reporting the intent as though it were the outcome is a confirmation of
// something nobody checked.
func TestResolveConfig_NoConfigSaysSo(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, note, err := bootstrap.ResolveConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repo != "." {
		t.Errorf("Repo = %q, want the built-in default", cfg.Repo)
	}
	if !strings.Contains(note, "built-in defaults") {
		t.Errorf("note = %q, want it to say the built-in defaults are in force", note)
	}
}

// A config that EXISTS but cannot be used must be an error, not a fallback.
//
// Falling back substitutes the built-in defaults, whose `repo: "."` is the WORKING
// DIRECTORY — so a typo would make enola analyse whichever repository you happen to
// be standing in and present the result as an answer about the one the config named.
// A missing file means "no preferences"; a broken one means the opposite.
func TestResolveConfig_UnusableConfigIsAnErrorNotAFallback(t *testing.T) {
	for _, tc := range []struct{ name, yaml string }{
		{"unparseable", "repo: [unterminated\n"},
		// Reached through Normalize rather than the YAML parser, and must not be
		// treated any more leniently: the file is present and says something enola
		// cannot honour.
		{"unusable output.dir", "repo: \".\"\noutput:\n  dir: \"/tmp/absolute\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "mcp-arch.yaml"), []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)

			cfg, note, err := bootstrap.ResolveConfig("")
			if err == nil {
				t.Fatalf("a broken config resolved to %#v with note %q, instead of erroring", cfg, note)
			}
			if cfg != nil {
				t.Errorf("a config was returned alongside the error: %#v", cfg)
			}
		})
	}
}

// The fallback is defensible for a distribution unpacked as a unit: binary and
// config shipped together, run from anywhere.
func TestResolveConfig_BundledConfigUsedWhenBinaryIsNotOnPATH(t *testing.T) {
	bundleConfig(t, "/from-bundle")
	t.Chdir(t.TempDir())
	t.Setenv("PATH", filepath.Join(t.TempDir(), "elsewhere"))

	cfg, note, err := bootstrap.ResolveConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repo != "/from-bundle" {
		t.Fatalf("Repo = %q, want the bundled config to apply to an unpacked binary", cfg.Repo)
	}
	// Silently inheriting configuration from wherever the binary happens to live is
	// the surprising half, so the note must say the config came from there.
	if !strings.Contains(note, "next to the enola binary") {
		t.Errorf("note = %q, want it to disclose that the config came from the binary's directory", note)
	}
}

// The regression this defends: a config beside an installed (or in-tree `go build`)
// binary governed every repository that binary was pointed at, from any directory
// with no config of its own — which is most of them. Being on PATH is what
// separates an installed binary from an unpacked bundle.
func TestResolveConfig_BundledConfigRefusedWhenBinaryIsOnPATH(t *testing.T) {
	bundleConfig(t, "/from-bundle")
	t.Chdir(t.TempDir())
	t.Setenv("PATH", exeDir(t)+string(os.PathListSeparator)+"/usr/bin")

	cfg, note, err := bootstrap.ResolveConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repo == "/from-bundle" {
		t.Errorf("a config beside a binary on PATH governed a repository elsewhere; note = %q", note)
	}
	if !strings.Contains(note, "built-in defaults") {
		t.Errorf("note = %q, want the built-in defaults", note)
	}
}

// An explicit --config path is the user naming a file; a fallback that quietly
// substituted a different one would make the flag a suggestion.
func TestResolveConfig_ExplicitAbsolutePathNeverFallsBack(t *testing.T) {
	bundleConfig(t, "/from-bundle")
	missing := filepath.Join(t.TempDir(), "no-such-config.yaml")

	cfg, _, err := bootstrap.ResolveConfig(missing)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repo == "/from-bundle" {
		t.Error("an explicit config path fell back to the config beside the binary")
	}
}
