// Package rustextractor extracts architectural facts from Rust source code
// using tree-sitter AST parsing (see rust_ast.go for the walker implementation
// and resolve.go for crate/import resolution helpers).
package rustextractor

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/parallel"
)

// RustExtractor extracts architectural facts from Rust source code.
type RustExtractor struct{}

// New creates a new RustExtractor.
func New() *RustExtractor {
	return &RustExtractor{}
}

func (e *RustExtractor) Name() string {
	return "rust"
}

// Detect returns true if the repository looks like a Rust project (a Cargo
// workspace or a single crate). It checks the repo root first, then walks up
// to 3 subdirectory levels to support monorepos where a Rust crate lives in a
// subdirectory, mirroring the Python extractor's monorepo detection.
func (e *RustExtractor) Detect(repoPath string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoPath, "Cargo.toml")); err == nil {
		return true, nil
	}
	found := false
	_ = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		rel, _ := filepath.Rel(repoPath, path)
		depth := strings.Count(filepath.ToSlash(rel), "/")
		if d.IsDir() {
			if depth >= 3 {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Base(path) == "Cargo.toml" {
			found = true
		}
		return nil
	})
	return found, nil
}

// isRustFile returns true if the file has a .rs extension.
func isRustFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".rs")
}

// OwnsFile implements plugin.FileOwner for incremental caching.
func (e *RustExtractor) OwnsFile(relFile string) bool { return isRustFile(relFile) }

// Extract parses Rust files and emits architectural facts.
//
// Every Cargo.toml in the repo is scanned first (cheap: no tree-sitter parse
// needed) to build a crate-name -> crate-directory index and the set of known
// module directories, both computed up front so cross-file import resolution
// (which crate/submodule a `use` path refers to) can happen synchronously
// during each file's walk rather than needing a second pass. `impl Trait for
// Type` is the one thing that genuinely needs a post-pass: the impl block is
// frequently declared in a different file than Type itself, so each file's
// walker only records the observation (astWalker.impls) and applyImplements
// attaches the resulting RelImplements edge to Type's own symbol fact once
// every file has been merged.
func (e *RustExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var rustFiles, cargoFiles []string
	for _, relFile := range files {
		switch {
		case isRustFile(relFile):
			rustFiles = append(rustFiles, relFile)
		case filepath.Base(relFile) == "Cargo.toml":
			cargoFiles = append(cargoFiles, relFile)
		}
	}

	crates := buildCrateIndex(repoPath, cargoFiles)

	moduleDirs := make(map[string]bool, len(rustFiles))
	for _, relFile := range rustFiles {
		moduleDirs[filepath.ToSlash(filepath.Dir(relFile))] = true
	}

	perFileFacts := parallel.MapFiles(ctx, rustFiles, func(relFile string) fileResult {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[rust-extractor] error reading %s: %v", relFile, err)
			return fileResult{}
		}
		ff, impls := extractFileAST(src, relFile, crates, moduleDirs)
		return fileResult{facts: ff, impls: impls}
	})

	var allFacts []facts.Fact
	var allImpls []implPair
	for _, r := range perFileFacts {
		allFacts = append(allFacts, r.facts...)
		allImpls = append(allImpls, r.impls...)
	}

	applyImplements(allFacts, allImpls)

	for dir := range moduleDirs {
		props := map[string]any{"language": "rust"}
		if crateDir := nearestCrateDir(dir, crates); crateDir != "" {
			if name, ok := crateNameByDir(crateDir, crates); ok {
				props["crate"] = name
			}
		}
		allFacts = append(allFacts, facts.Fact{
			Kind:  facts.KindModule,
			Name:  dir,
			File:  dir,
			Props: props,
		})
	}

	return allFacts, nil
}

// fileResult holds one file's extracted facts plus its impl-block
// observations, returned together from the parallel per-file walk.
type fileResult struct {
	facts []facts.Fact
	impls []implPair
}
