package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBundledConfigCoversDefaultIgnores keeps the shipped mcp-arch.yaml from
// drifting behind config.Default().Ignore.
//
// The bundled file is not just an example: README tells users to curl it straight
// into their repo, and config is a full OVERRIDE rather than a merge — so anything
// present in the built-in defaults but missing here is silently NOT ignored for
// every user who adopts the file. It had already drifted: the two Ruby spec globs
// were absent despite the "kept in sync" comments, so adopters indexed RSpec and
// Minitest files as production code, and dist/, coverage/ and target/ build output
// went unignored too.
//
// The file is deliberately a SUPERSET (Android/Gradle, Xcode/SPM, Rails, CI, Docker
// …), so this asserts containment, not equality.
func TestBundledConfigCoversDefaultIgnores(t *testing.T) {
	path := filepath.Join("..", "..", "mcp-arch.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	bundled := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if item, ok := yamlListItem(line); ok {
			bundled[item] = true
		}
	}
	if len(bundled) == 0 {
		t.Fatalf("parsed no ignore entries from %s — the parser or the file shape changed", path)
	}

	for _, want := range Default().Ignore {
		if !bundled[want] {
			t.Errorf("mcp-arch.yaml is missing %q from config.Default().Ignore; "+
				"users who adopt the bundled file will not ignore it", want)
		}
	}
}

// TestBundledConfigDoesNotPinPluginLists keeps the shipped mcp-arch.yaml from
// naming extractors, explainers or renderers at all.
//
// Same override-not-merge rule as the ignore list above, but with the opposite
// remedy, because these lists cannot be written as a superset: a file that mirrors
// config.Default() can only ever fall behind it, and falling behind means an
// extractor that exists is silently never run. It had already happened — the file
// listed eleven extractors and omitted rust from the day the Rust extractor landed,
// so a 780-file Rust repository reported 0 facts with no error and no mention of
// Rust anywhere. Omitting the keys is what makes new plugins arrive automatically.
//
// Asserting the loaded lists match Default() would pass on a file that pins today's
// exact list, which is the state this is here to prevent, so absence is asserted
// directly.
func TestBundledConfigDoesNotPinPluginLists(t *testing.T) {
	path := filepath.Join("..", "..", "mcp-arch.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, key := range []string{"extractors:", "explainers:", "renderers:"} {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, key) {
				t.Errorf("mcp-arch.yaml declares %s — it replaces the built-in list rather "+
					"than extending it, so it can only fall behind config.Default() and "+
					"silently disable plugins that ship later", key)
			}
		}
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	if cfg.ExtractorsExplicit {
		t.Error("bundled config is treated as an explicit extractor list")
	}
	if got, want := strings.Join(cfg.Extractors, ","), strings.Join(Default().Extractors, ","); got != want {
		t.Errorf("bundled config yields extractors %q, want the defaults %q", got, want)
	}
	if got, want := strings.Join(cfg.Explainers, ","), strings.Join(Default().Explainers, ","); got != want {
		t.Errorf("bundled config yields explainers %q, want the defaults %q", got, want)
	}
	if got, want := strings.Join(cfg.Renderers, ","), strings.Join(Default().Renderers, ","); got != want {
		t.Errorf("bundled config yields renderers %q, want the defaults %q", got, want)
	}
}

// TestExampleConfigsDoNotPinExplainersOrRenderers extends the rule above to
// examples/, which is where it did the most damage.
//
// Every example pinned four explainers, so every user who adopted one lost the
// other six — god-class, hotspots, unused-routes, dependency-depth,
// exported-surface, complexity-outliers — without ever being told an explainer
// existed. The four listed were a strict subset of the defaults, so the keys bought
// nothing in exchange.
//
// `extractors:` is exempt: narrowing to one language is what the per-language
// examples are FOR. full.yaml is not exempt — it claims to enable everything, and a
// list claiming to be exhaustive is precisely the one that falls behind (it omitted
// grpc, openapi and python).
func TestExampleConfigsDoNotPinExplainersOrRenderers(t *testing.T) {
	dir := filepath.Join("..", "..", "examples")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	checked := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		checked++
		t.Run(e.Name(), func(t *testing.T) {
			path := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			banned := []string{"explainers:", "renderers:"}
			if e.Name() == "full.yaml" {
				banned = append(banned, "extractors:")
			}
			for _, key := range banned {
				for _, line := range strings.Split(string(raw), "\n") {
					if strings.HasPrefix(line, key) {
						t.Errorf("%s declares %s — it replaces the built-in list rather than "+
							"extending it, so every user who adopts this file loses whatever "+
							"ships later", e.Name(), key)
					}
				}
			}
			// The example must still be loadable: a config nobody can parse is a
			// worse starting point than none.
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.Explainers) != len(Default().Explainers) {
				t.Errorf("loads %d explainers, want the %d defaults", len(cfg.Explainers), len(Default().Explainers))
			}
		})
	}
	if checked == 0 {
		t.Fatalf("no example configs found under %s — the layout changed", dir)
	}
}

// TestLoadRecordsExtractorsExplicit — the engine's shadowed-extractor warning fires
// only for a hand-written list, so "the file said so" must survive Load. It cannot
// be recovered afterwards: an absent key leaves Default()'s list in place, and a
// list identical to the defaults is indistinguishable from inheriting them.
func TestLoadRecordsExtractorsExplicit(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want bool
	}{
		{"absent", "repo: \".\"\n", false},
		{"listed", "repo: \".\"\nextractors:\n  - go\n", true},
		// An empty list is a real choice ("extract nothing"), not an absent key.
		{"empty list", "repo: \".\"\nextractors: []\n", true},
		// Mirroring the defaults is the case that motivated the flag: it looks
		// identical to inheriting them, and stops looking identical the day a new
		// extractor ships.
		{"mirrors defaults", "repo: \".\"\nextractors:\n" + defaultExtractorsYAML(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "mcp-arch.yaml")
			if err := os.WriteFile(p, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(p)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.ExtractorsExplicit != tc.want {
				t.Errorf("ExtractorsExplicit = %v, want %v", cfg.ExtractorsExplicit, tc.want)
			}
		})
	}
}

func defaultExtractorsYAML() string {
	var sb strings.Builder
	for _, e := range Default().Extractors {
		sb.WriteString("  - " + e + "\n")
	}
	return sb.String()
}

// yamlListItem returns the unquoted value of a `  - "x"` list line. Comments and
// any other line shape return false.
func yamlListItem(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "- ") {
		return "", false
	}
	v := strings.TrimSpace(strings.TrimPrefix(t, "- "))
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return "", false
	}
	return v[1 : len(v)-1], true
}
