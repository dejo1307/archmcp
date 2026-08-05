// Package mdintent extracts declared intent from markdown pages — the wiki as
// a source tree, compiled by enola like any other source. A page opts in with
// an `enola_intent:` frontmatter key (namespaced so the wiki's own toolchain
// ignores it); the page body is prose and is never parsed. Every fact this
// extractor emits cites the PAGE as its file, so a verdict's evidence points
// at the decision that declared the intent, not at a config artifact.
package mdintent

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
	"github.com/enola-labs/enola/pkg/plugin"
)

// Extractor extracts enola_intent frontmatter from markdown pages.
type Extractor struct{}

// New creates the extractor.
func New() *Extractor { return &Extractor{} }

// Name returns the extractor name.
func (e *Extractor) Name() string { return "mdintent" }

// Detect probes for a markdown file carrying the enola_intent key, up to five
// directory levels — wiki trees nest (wiki/<scope>/permanent/<area>/page.md).
func (e *Extractor) Detect(repoPath string) (bool, error) {
	found := false
	root := filepath.Clean(repoPath)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || mdSkipDirs[name]) {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(root, path)
			if rel != "." && strings.Count(filepath.ToSlash(rel), "/") >= 5 {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			if src, rerr := os.ReadFile(path); rerr == nil &&
				strings.Contains(string(src[:min(len(src), 4096)]), intent.PageIntentKey+":") {
				found = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found, nil
}

var mdSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "testdata": true, "dist": true,
	"build": true, "tmp": true, "_archive": true, "_views": true,
}

// Extract parses every markdown page's frontmatter and compiles enola_intent
// blocks into intent facts. An invalid block fails the snapshot — a
// declaration that cannot be trusted is worse than none.
func (e *Extractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var out []facts.Fact
	for _, relFile := range files {
		slashed := filepath.ToSlash(relFile)
		if !strings.HasSuffix(slashed, ".md") ||
			strings.Contains(slashed, "_archive/") || strings.Contains(slashed, "_views/") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			continue
		}
		page, err := intent.ParsePage(src)
		if err != nil {
			// Fatal, not recorded: an ordinary extractor error loses one file's
			// facts, but losing a declaration loses every verdict computed from it
			// while the snapshot still reports success. The repo-file carrier fails
			// the run for the same reason; both carriers must behave alike, or which
			// file you put a declaration in decides whether a typo is silent.
			return nil, plugin.Fatalf("%s: %w", relFile, err)
		}
		if page == nil {
			continue
		}
		out = append(out, intent.CompilePageFacts(page, slashed)...)
		rels := 0
		if page.Page != nil {
			rels = len(page.Page.Relations)
		}
		log.Printf("[mdintent] %s: page=%t %d relation(s), %d seam(s), %d layer set(s), %d claim(s)",
			relFile, page.Page != nil, rels, len(page.Consumes), len(page.Layers), len(page.Claims))
	}
	return out, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
