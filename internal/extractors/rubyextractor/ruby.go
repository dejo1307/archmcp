package rubyextractor

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/parallel"
)

// RubyExtractor extracts architectural facts from Ruby source code using the
// tree-sitter Ruby grammar (in line with the other language extractors).
type RubyExtractor struct{}

// New creates a new RubyExtractor.
func New() *RubyExtractor {
	return &RubyExtractor{}
}

func (e *RubyExtractor) Name() string {
	return "ruby"
}

// Detect returns true if the repository looks like a Ruby project (has a Gemfile).
func (e *RubyExtractor) Detect(repoPath string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoPath, "Gemfile")); err == nil {
		return true, nil
	}
	return false, nil
}

// Extract parses Ruby files and emits architectural facts.
func (e *RubyExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var allFacts []facts.Fact

	isRails := detectRailsProject(repoPath)

	// Pass 1: parse packwerk packages (builds package map and privacy boundaries).
	pkgInfo := parsePackwerk(repoPath)
	allFacts = append(allFacts, pkgInfo.facts...)

	// Pass 2: parse .rb files. Route files are parsed separately by the route
	// extractor, so they are excluded here.
	var rbFiles []string
	for _, relFile := range files {
		if !isRubyFile(relFile) {
			continue
		}
		if isRails && isRouteFile(relFile) {
			continue
		}
		rbFiles = append(rbFiles, relFile)
	}

	// pkgInfo is read-only here, so per-file parsing is independent. Parse in
	// parallel and merge in file order for deterministic output.
	perFileFacts := parallel.MapFiles(ctx, rbFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[ruby-extractor] error reading %s: %v", relFile, err)
			return nil
		}
		exported := isPublicAPI(relFile, pkgInfo)
		// extractFileAST emits symbols, imports, mixins, constants, attrs, calls,
		// and ActiveRecord storage/associations in a single AST pass;
		// extractRubyHTTPClientFacts adds outbound HTTP-client routes.
		ff := extractFileAST(src, relFile, isRails, exported)
		return append(ff, extractRubyHTTPClientFacts(src, relFile)...)
	})

	// Track directories that contain Ruby files for module emission.
	modules := make(map[string]bool)
	for i, ff := range perFileFacts {
		allFacts = append(allFacts, ff...)
		modules[filepath.Dir(rbFiles[i])] = true
	}

	// Emit module facts for directories not already covered by packwerk packages.
	for dir := range modules {
		if pkgInfo.isPackage(dir) {
			continue
		}
		props := map[string]any{
			"language": "ruby",
		}
		if isRails {
			props["framework"] = "rails"
		}
		allFacts = append(allFacts, facts.Fact{
			Kind:  facts.KindModule,
			Name:  dir,
			File:  dir,
			Props: props,
		})
	}

	// Parse Rails route files.
	if isRails {
		routeFacts := extractAllRoutes(repoPath, files)
		allFacts = append(allFacts, routeFacts...)
	}

	// Resolve constant references (inheritance, mixins, associations, calls),
	// require_relative paths, and Packwerk dependencies into internal module
	// coupling edges. Without this, Ruby imports never match module Names
	// downstream and coupling collapses to zero.
	allFacts = append(allFacts, resolveImports(allFacts, isRails)...)

	return allFacts, nil
}

// ExtractTestRefs implements plugin.TestRefExtractor. It parses test/spec files
// for the SOLE purpose of capturing their outbound references into production
// code, emitting one facts.KindTestRef fact per file that carries only RelCalls
// edges — no symbols. Test methods therefore never become dead-code candidates
// (which the orphans package explicitly excludes), and no symbol/module/route
// explainer is affected, while the dead-code detector can still see that a
// production symbol is exercised by a test and not mis-report it as dead.
func (e *RubyExtractor) ExtractTestRefs(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var rbFiles []string
	for _, relFile := range files {
		if isRubyFile(relFile) {
			rbFiles = append(rbFiles, relFile)
		}
	}
	perFile := parallel.MapFiles(ctx, rbFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[ruby-extractor] error reading test file %s: %v", relFile, err)
			return nil
		}
		return extractTestRefsAST(src, relFile)
	})
	var out []facts.Fact
	for _, ff := range perFile {
		out = append(out, ff...)
	}
	return out, nil
}

// --- Rails detection ---

func detectRailsProject(repoPath string) bool {
	candidates := []string{
		filepath.Join(repoPath, "config", "application.rb"),
		filepath.Join(repoPath, "bin", "rails"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// isRubyFile returns true if the file is Ruby source: a .rb/.rake file or a
// Rakefile. Rake tasks are Ruby and call into app code (e.g. from lib/tasks/),
// so indexing them lets those calls resolve (dead-code precision).
func isRubyFile(path string) bool {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".rb") || strings.HasSuffix(lower, ".rake") {
		return true
	}
	return filepath.Base(path) == "Rakefile"
}

// OwnsFile implements plugin.FileOwner for incremental caching.
func (e *RubyExtractor) OwnsFile(relFile string) bool { return isRubyFile(relFile) }

// isPublicAPI checks if a file is within a packwerk package's app/public/ directory.
func isPublicAPI(relFile string, pkg *packwerkInfo) bool {
	if pkg == nil || len(pkg.packages) == 0 {
		return true
	}

	ownerPkg := pkg.ownerPackage(relFile)
	if ownerPkg == "" {
		return true
	}

	pkgCfg, ok := pkg.packages[ownerPkg]
	if !ok || !pkgCfg.enforcePrivacy {
		return true
	}

	publicDir := filepath.Join(ownerPkg, "app", "public")
	return strings.HasPrefix(relFile, publicDir+"/") || strings.HasPrefix(relFile, publicDir+"\\")
}
