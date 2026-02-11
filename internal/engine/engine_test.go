package engine

import (
	"testing"

	"github.com/dejo1307/archmcp/internal/config"
)

func TestIsIgnored(t *testing.T) {
	tests := []struct {
		name     string
		relPath  string
		isDir    bool
		patterns []string
		want     bool
	}{
		{
			"vendor directory",
			"vendor/foo/bar.go", false,
			[]string{"vendor/**"},
			true,
		},
		{
			"vendor dir itself",
			"vendor", true,
			[]string{"vendor/**"},
			true,
		},
		{
			"node_modules",
			"node_modules/react/index.js", false,
			[]string{"node_modules/**"},
			true,
		},
		{
			"git directory",
			".git/HEAD", false,
			[]string{".git/**"},
			true,
		},
		{
			"test files with ** prefix",
			"src/main_test.go", false,
			[]string{"**/*_test.go"},
			true,
		},
		{
			"non-test file not ignored",
			"src/main.go", false,
			[]string{"**/*_test.go"},
			false,
		},
		{
			"spec files",
			"src/utils.spec.ts", false,
			[]string{"**/*.spec.ts"},
			true,
		},
		{
			"archmcp output dir",
			".archmcp/facts.jsonl", false,
			[]string{".archmcp/**"},
			true,
		},
		{
			"normal source not ignored",
			"src/app.go", false,
			[]string{"vendor/**"},
			false,
		},
		{
			"build directory",
			"build/output.kt", false,
			[]string{"build/**"},
			true,
		},
		{
			"nested test file",
			"internal/pkg/foo_test.go", false,
			[]string{"**/*_test.go"},
			true,
		},
		{
			"deeply nested vendor",
			"vendor/github.com/foo/bar/baz.go", false,
			[]string{"vendor/**"},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Ignore = tt.patterns

			eng, _ := New(cfg)
			got := eng.isIgnored(tt.relPath, tt.isDir)
			if got != tt.want {
				t.Errorf("isIgnored(%q, isDir=%v) with patterns %v = %v, want %v",
					tt.relPath, tt.isDir, tt.patterns, got, tt.want)
			}
		})
	}
}
