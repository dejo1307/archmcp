package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestArchitectureDocMatchesDefaults keeps the documentation honest about which
// extractors are on by default.
//
// This list has now drifted twice — `php` was added at cacheVersion v5 and `grpc` at
// v73, and neither updated ARCHITECTURE.md — leaving the doc claiming 9 extractors
// where config.Default() enables 11. That is not cosmetic: config is a full OVERRIDE,
// not a merge (IsExtractorEnabled tests membership of the listed set), so a user who
// copies the doc's example block into their own mcp-arch.yaml SILENTLY DISABLES gRPC
// and PHP extraction. Their PHP repo then yields no facts, with nothing to explain why.
//
// A doc that drifts twice will drift again, so the doc is now a test fixture.
func TestArchitectureDocMatchesDefaults(t *testing.T) {
	path := filepath.Join("..", "..", "ARCHITECTURE.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := string(raw)

	want := Default().Extractors

	t.Run("yaml example block", func(t *testing.T) {
		got := extractorsFromYAMLBlock(t, doc)
		assertSameSet(t, got, want, "the `extractors:` example block in ARCHITECTURE.md")
	})

	t.Run("config reference table", func(t *testing.T) {
		got := extractorsFromTableRow(t, doc)
		assertSameSet(t, got, want, "the `extractors` row of the config-reference table")
	})
}

// extractorsFromYAMLBlock reads the list items under the first `extractors:` line.
func extractorsFromYAMLBlock(t *testing.T, doc string) []string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	var out []string
	for i, l := range lines {
		if strings.TrimSpace(l) != "extractors:" {
			continue
		}
		for _, item := range lines[i+1:] {
			trimmed := strings.TrimSpace(item)
			if !strings.HasPrefix(trimmed, "- ") {
				break
			}
			out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
		}
		return out
	}
	t.Fatal("no `extractors:` block found in ARCHITECTURE.md")
	return nil
}

// extractorsFromTableRow reads the quoted list out of the `| `extractors` | … |` row.
func extractorsFromTableRow(t *testing.T, doc string) []string {
	t.Helper()
	re := regexp.MustCompile("(?m)^\\|\\s*`extractors`\\s*\\|[^|]*\\|\\s*`\\[([^\\]]*)\\]`")
	m := re.FindStringSubmatch(doc)
	if m == nil {
		t.Fatal("no `extractors` row found in the ARCHITECTURE.md config table")
	}
	var out []string
	for _, f := range strings.Split(m[1], ",") {
		if v := strings.Trim(strings.TrimSpace(f), `"`); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func assertSameSet(t *testing.T, got, want []string, where string) {
	t.Helper()
	inWant := map[string]bool{}
	for _, w := range want {
		inWant[w] = true
	}
	inGot := map[string]bool{}
	for _, g := range got {
		inGot[g] = true
	}
	for _, w := range want {
		if !inGot[w] {
			t.Errorf("%s omits %q — a user copying it would silently DISABLE that extractor "+
				"(config is a full override, not a merge)", where, w)
		}
	}
	for _, g := range got {
		if !inWant[g] {
			t.Errorf("%s lists %q, which config.Default() does not enable", where, g)
		}
	}
}
