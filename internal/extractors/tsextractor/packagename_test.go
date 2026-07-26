package tsextractor

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// extractRepoAllFiles runs the extractor with the FULL repo file list (as the
// engine's walkRepo produces), not just the TypeScript files — package.json is
// read off-glob and must be present for package_name to be emitted.
func extractRepoAllFiles(t *testing.T, files map[string]string) []facts.Fact {
	t.Helper()
	dir := t.TempDir()
	rel := make([]string, 0, len(files))
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		rel = append(rel, name)
	}
	sort.Strings(rel)
	got, err := New().Extract(context.Background(), dir, rel)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return got
}

func moduleProp(ff []facts.Fact, dir, prop string) any {
	for _, f := range ff {
		if f.Kind == facts.KindModule && f.Name == dir {
			return f.Props[prop]
		}
	}
	return nil
}

// A module fact carries the name of the npm package its directory belongs to.
// The cross-repo import linker reads it to recognize the repo's own @scope.
func TestExtract_ModulePackageName(t *testing.T) {
	ff := extractRepoAllFiles(t, map[string]string{
		"package.json": `{"name": "@acme/sdk", "version": "1.0.0"}`,
		"src/index.ts": "export const x = 1;\n",
	})
	if got := moduleProp(ff, "src", "package_name"); got != "@acme/sdk" {
		t.Errorf("package_name = %v, want @acme/sdk", got)
	}
}

// In a monorepo each directory resolves to its NEAREST package.json, the same
// rule npm itself applies — not the repo root's.
func TestExtract_ModulePackageNameNearestAncestor(t *testing.T) {
	ff := extractRepoAllFiles(t, map[string]string{
		"package.json":              `{"name": "@acme/root"}`,
		"packages/api/package.json": `{"name": "@acme/api"}`,
		"packages/api/src/index.ts": "export const a = 1;\n",
		"tools/build.ts":            "export const b = 2;\n",
	})
	if got := moduleProp(ff, "packages/api/src", "package_name"); got != "@acme/api" {
		t.Errorf("packages/api/src package_name = %v, want @acme/api", got)
	}
	// No package.json under tools/ — falls back to the root package.
	if got := moduleProp(ff, "tools", "package_name"); got != "@acme/root" {
		t.Errorf("tools package_name = %v, want @acme/root", got)
	}
}
