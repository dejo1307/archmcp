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
