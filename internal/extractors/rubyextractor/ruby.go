package rubyextractor

import (
	"bufio"
	"context"
	"io"
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

// Detect returns true if the repository looks like a Ruby project. A root
// Gemfile is the fast path; failing that (many Ruby CLIs/plugins ship no
// Gemfile), it falls back to a bounded scan for loose Ruby files — .rb/.rake
// sources or extensionless executables with a Ruby shebang — mirroring the PHP
// extractor's containsPHPFile fallback so Gemfile-less repos still get indexed.
func (e *RubyExtractor) Detect(repoPath string) (bool, error) {
	if _, err := os.Stat(filepath.Join(repoPath, "Gemfile")); err == nil {
		return true, nil
	}
	return containsRubyFile(repoPath, 3), nil
}

// containsRubyFile reports whether a Ruby file exists within maxDepth directory
// levels of root (0 = root only). Vendored and VCS directories are skipped.
// A file counts as Ruby if isRubyFile matches its name (.rb/.rake/Rakefile) or,
// for an extensionless file, it carries a Ruby shebang.
func containsRubyFile(root string, maxDepth int) bool {
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
			if isRubyFile(name) || (filepath.Ext(name) == "" && hasRubyShebang(filepath.Join(dir, name))) {
				found = true
				return
			}
		}
	}
	walk(root, 0)
	return found
}

// hasRubyShebang reports whether the file at absPath begins with a Ruby shebang
// (e.g. "#!/usr/bin/env ruby"). Only the first line is read, so this is cheap
// enough to run over extensionless files during discovery. Non-Ruby shebangs
// (bash, node, …) and binary files return false.
func hasRubyShebang(absPath string) bool {
	f, err := os.Open(absPath)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(io.LimitReader(f, 256))
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "#!") && strings.Contains(line, "ruby")
}

// isRubySourceFile reports whether relFile should be parsed as Ruby: any file
// isRubyFile matches by extension (no I/O), or an extensionless file carrying a
// Ruby shebang. repoPath is needed to resolve the shebang read.
func isRubySourceFile(repoPath, relFile string) bool {
	if isRubyFile(relFile) {
		return true
	}
	if filepath.Ext(relFile) == "" {
		return hasRubyShebang(filepath.Join(repoPath, relFile))
	}
	return false
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
		if !isRubySourceFile(repoPath, relFile) {
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
		ff = append(ff, extractRubyHTTPClientFacts(src, relFile)...)
		ff = append(ff, extractGraphQLRubyRoutes(src, relFile)...)
		return append(ff, extractGraphQLRubyClientOps(src, relFile)...)
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
		role := facts.ModuleRoleForPath(dir)
		if isRails {
			// Rails has directories the language-agnostic classifier cannot know about.
			// db/migrate is the one that matters: a migration is a one-shot script that
			// nothing references by design, so leaving it as production code makes every
			// migration in the repository a dead-code candidate.
			if r := railsModuleRole(dir); r != "" {
				role = r
			}
		}
		props := map[string]any{
			"language":    "ruby",
			"module_role": role,
		}
		if isRails {
			props["framework"] = "rails"
			if c := railsComponentForPath(dir); c != "" {
				props["rails_component"] = c
			}
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

	// Grape APIs. Not gated on isRails — Grape is a Rack framework and a Grape-only
	// service has no Rails markers at all. It runs after the AST pass because a Grape
	// class is identified by transitive inheritance, which is a repo-wide question the
	// per-file pass cannot answer; the class facts just produced are its input, so this
	// costs no extra reads on a repository that contains no Grape.
	allFacts = append(allFacts, extractGrapeRoutes(ctx, repoPath, allFacts)...)

	// Extract Ruby calls embedded in view templates (ERB/Slim/HAML) so helpers and
	// class methods invoked only from views are not mis-reported as dead. Emits
	// reference-only KindFileRef facts (no symbols); parsed in parallel.
	var tmplFiles []string
	for _, relFile := range files {
		if isTemplateFile(relFile) {
			tmplFiles = append(tmplFiles, relFile)
		}
	}
	tmplFacts := parallel.MapFiles(ctx, tmplFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[ruby-extractor] error reading template %s: %v", relFile, err)
			return nil
		}
		return extractTemplateRefs(src, relFile)
	})
	for _, ff := range tmplFacts {
		allFacts = append(allFacts, ff...)
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
// prodFiles is unused: Ruby references are resolved by constant name, not against
// the set of files that exist.
func (e *RubyExtractor) ExtractTestRefs(ctx context.Context, repoPath string, testFiles, _ []string) ([]facts.Fact, error) {
	var rbFiles []string
	for _, relFile := range testFiles {
		if isRubySourceFile(repoPath, relFile) {
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

// detectRailsProject reports whether the repository is Rails. The two root markers are
// the fast path — an application always has them — but a repository of mountable
// ENGINES has neither: solidus is six engines and a Gemfile with no root config/ at
// all, so the root-only check called it "not Rails" and every Rails-conditional fact
// (route files, the framework prop that gates the rails-mvc layer pattern) was skipped
// for a codebase that is nothing but Rails.
//
// So a third marker: any engine directory that carries both a config/routes.rb and a
// lib/**/engine.rb. That pair is Rails by construction and cannot be produced by a
// plain Ruby gem.
func detectRailsProject(repoPath string) bool {
	candidates := []string{
		filepath.Join(repoPath, "config", "application.rb"),
		filepath.Join(repoPath, "bin", "rails"),
		filepath.Join(repoPath, "config", "environment.rb"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return containsRailsEngine(repoPath)
}

// containsRailsEngine reports whether any immediate subdirectory of root is a Rails
// engine: it has config/routes.rb and at least one engine.rb under lib/. Bounded to one
// level below the root, which is where a gem monorepo puts its engines.
func containsRailsEngine(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, ent := range entries {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") || ent.Name() == "vendor" {
			continue
		}
		dir := filepath.Join(root, ent.Name())
		if _, err := os.Stat(filepath.Join(dir, "config", "routes.rb")); err != nil {
			continue
		}
		if hasEngineFile(filepath.Join(dir, "lib"), 3) {
			return true
		}
	}
	return false
}

// hasEngineFile reports whether an engine.rb exists within maxDepth levels of dir.
func hasEngineFile(dir string, maxDepth int) bool {
	if maxDepth < 0 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, ent := range entries {
		if ent.IsDir() {
			if hasEngineFile(filepath.Join(dir, ent.Name()), maxDepth-1) {
				return true
			}
			continue
		}
		if ent.Name() == "engine.rb" {
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

// OwnsFile implements plugin.FileOwner for incremental caching. It is
// extension-only (no repoPath is available to sniff shebangs), so extensionless
// Ruby executables are not tracked for incremental cache invalidation; edits to
// them won't invalidate the cache key on their own. This is acceptable — such
// files are rare and the cacheVersion bump forces a full re-extract when the
// extractor's behavior changes.
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
