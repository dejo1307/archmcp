// Package phpextractor extracts architectural facts from PHP source code using
// the tree-sitter PHP grammar, in line with the other language extractors. It
// emits symbols (classes, interfaces, traits, enums, functions, methods,
// constants), `use` import dependencies, a call / instantiation graph, inheritance
// edges, and per-function cyclomatic complexity. When the repository is detected as
// WordPress, hook registrations and hook points (add_action / add_filter /
// do_action / apply_filters / register_rest_route) are emitted as route facts.
package phpextractor

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/parallel"
)

// PHPExtractor extracts architectural facts from PHP source code.
type PHPExtractor struct{}

// New creates a new PHPExtractor.
func New() *PHPExtractor {
	return &PHPExtractor{}
}

func (e *PHPExtractor) Name() string {
	return "php"
}

// wordpressMarkers are bootstrap files unique to a WordPress install/core. Their
// presence both confirms a PHP project and enables WordPress hook awareness.
var wordpressMarkers = []string{"wp-load.php", "wp-settings.php", "wp-config.php", "wp-blog-header.php"}

// Detect returns true if the repository looks like a PHP project: it has a
// composer.json, a WordPress bootstrap file, or any .php file within a few
// directory levels (generic PHP projects often ship neither manifest).
func (e *PHPExtractor) Detect(repoPath string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoPath, "composer.json")); err == nil {
		return true, nil
	}
	for _, m := range wordpressMarkers {
		if _, err := os.Stat(filepath.Join(repoPath, m)); err == nil {
			return true, nil
		}
	}
	return containsPHPFile(repoPath, 3), nil
}

// containsPHPFile reports whether a .php file exists within maxDepth directory
// levels of root (0 = root only). Vendored and VCS directories are skipped.
func containsPHPFile(root string, maxDepth int) bool {
	var found bool
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if found || depth > maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, ent := range entries {
			if found {
				return
			}
			name := ent.Name()
			if ent.IsDir() {
				if name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
					continue
				}
				walk(filepath.Join(dir, name), depth+1)
				continue
			}
			if isPHPFile(name) {
				found = true
				return
			}
		}
	}
	walk(root, 0)
	return found
}

// detectWordPress reports whether the repository is a WordPress codebase, which
// turns on hook (add_action / add_filter / …) route extraction.
func detectWordPress(repoPath string) bool {
	for _, m := range wordpressMarkers {
		if _, err := os.Stat(filepath.Join(repoPath, m)); err == nil {
			return true
		}
	}
	// WordPress core keeps its bootstrap files under src/ in the develop checkout.
	for _, m := range wordpressMarkers {
		if _, err := os.Stat(filepath.Join(repoPath, "src", m)); err == nil {
			return true
		}
	}
	return false
}

// Extract parses PHP files and emits architectural facts.
func (e *PHPExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	isWordPress := detectWordPress(repoPath)

	var phpFiles []string
	for _, relFile := range files {
		if isPHPFile(relFile) {
			phpFiles = append(phpFiles, relFile)
		}
	}

	// Per-file parsing is independent. Parse in parallel and merge in file order
	// for deterministic output.
	perFileFacts := parallel.MapFiles(ctx, phpFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[php-extractor] error reading %s: %v", relFile, err)
			return nil
		}
		// extractFileAST emits symbols, use-imports, inheritance, calls, and
		// complexity. extractHooks adds WordPress hook routes (a no-op for
		// non-WordPress repos when isWordPress is false).
		ff := extractFileAST(src, relFile)
		if isWordPress {
			ff = append(ff, extractHooks(src, relFile)...)
		}
		return ff
	})

	var allFacts []facts.Fact
	modules := make(map[string]bool)
	for i, ff := range perFileFacts {
		allFacts = append(allFacts, ff...)
		modules[filepath.Dir(phpFiles[i])] = true
	}

	// Emit one module fact per directory containing PHP files.
	for dir := range modules {
		props := map[string]any{"language": "php"}
		if isWordPress {
			props["framework"] = "wordpress"
		}
		allFacts = append(allFacts, facts.Fact{
			Kind:  facts.KindModule,
			Name:  dir,
			File:  dir,
			Props: props,
		})
	}

	// Resolve `use` imports and namespaced references (inheritance, calls,
	// instantiations) into internal module-coupling edges. Without this, PHP
	// imports never match module Names downstream and coupling collapses to zero.
	allFacts = append(allFacts, resolveImports(allFacts, isWordPress)...)

	return allFacts, nil
}

// OwnsFile implements plugin.FileOwner for incremental caching.
func (e *PHPExtractor) OwnsFile(relFile string) bool { return isPHPFile(relFile) }

// isPHPFile returns true if the path has a PHP extension.
func isPHPFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php", ".phtml", ".php4", ".php5", ".php7", ".phps":
		return true
	}
	return false
}
