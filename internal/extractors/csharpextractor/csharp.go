// Package csharpextractor extracts architectural facts from C# source code
// using tree-sitter AST parsing (see csharp_ast.go for the walker and
// resolve.go for the project index, type resolution and partial-type merge).
//
// The grammar is pinned to tree-sitter-c-sharp v0.23.1, which is NOT the latest.
// v0.23.2 and above are generated against tree-sitter ABI 15, while the vendored
// go-tree-sitter runtime accepts at most 14, so SetLanguage rejects them. The
// failure is silent by nature — every file parses to nothing, which looks exactly
// like a repository containing no C# — so extractFileAST logs once when the
// grammar is refused rather than returning an empty result unremarked.
package csharpextractor

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

// CSharpExtractor extracts architectural facts from C# source code.
type CSharpExtractor struct{}

// New creates a new CSharpExtractor.
func New() *CSharpExtractor {
	return &CSharpExtractor{}
}

func (e *CSharpExtractor) Name() string { return "csharp" }

// detectMaxDepth bounds Detect's walk. A .NET solution puts its project files
// under src/<Project>/<Project>.csproj at most a few levels down; walking the
// whole tree of a repository the size of dotnet/runtime (32k sources, 6k project
// files) to answer a yes/no question is wasted work.
const detectMaxDepth = 4

// Detect returns true if the repository looks like a .NET project: an MSBuild
// solution or project file, or any real C# source within detectMaxDepth levels.
//
// A project file alone is not required, mirroring the Java extractor's reasoning
// in reverse: `global.json` and `Directory.Build.props` are .NET-only but appear
// in repositories that vendor a .NET tool without containing C#, while a `.cs`
// file is unambiguous — no other ecosystem uses that extension.
func (e *CSharpExtractor) Detect(repoPath string) (bool, error) {
	found := false
	_ = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		rel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			// Never descend into build output: obj/ holds generated C# that would
			// make any tree with a stale build directory look like a C# project.
			switch d.Name() {
			case "obj", "bin", "node_modules", ".git":
				return filepath.SkipDir
			}
			if strings.Count(filepath.ToSlash(rel), "/") >= detectMaxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".cs", ".csproj", ".sln", ".slnx":
			found = true
		}
		return nil
	})
	return found, nil
}

func isCSharpFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".cs")
}

func isProjectFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".csproj")
}

// OwnsFile implements plugin.FileOwner for incremental caching. Project files are
// owned alongside sources because they seed the project index every module fact
// reads, so adding or renaming a .csproj must invalidate the cache.
func (e *CSharpExtractor) OwnsFile(relFile string) bool {
	return isCSharpFile(relFile) || isProjectFile(relFile)
}

// Extract parses C# files and emits architectural facts.
//
// Three phases. Every .csproj path is scanned first (path-only: no XML parsing)
// into a project-directory index, so each file's walk can name its owning project
// synchronously. Files are then parsed in parallel — extractFileAST is a pure
// function of (src, relFile, project) — and merged in file order. The remaining
// work genuinely needs the whole merged fact set: partial types are declared
// across files by design, and C# `using` imports a NAMESPACE rather than a type,
// so a bare type reference cannot be resolved from one file's imports the way
// Java's can. Both are settled in resolveCSharp once every file is in.
func (e *CSharpExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var csFiles, projFiles []string
	for _, relFile := range files {
		switch {
		case isCSharpFile(relFile):
			csFiles = append(csFiles, relFile)
		case isProjectFile(relFile):
			projFiles = append(projFiles, relFile)
		}
	}

	projects := buildProjectIndex(projFiles)

	perFileFacts := parallel.MapFiles(ctx, csFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[csharp-extractor] error reading %s: %v", relFile, err)
			return nil
		}
		if isGeneratedSource(relFile, src) {
			return nil
		}
		return extractFileAST(src, relFile)
	})

	var allFacts []facts.Fact
	modules := make(map[string]bool)
	parsed := 0
	for i, ff := range perFileFacts {
		if ff == nil {
			continue
		}
		parsed++
		allFacts = append(allFacts, ff...)
		modules[filepath.ToSlash(filepath.Dir(csFiles[i]))] = true
	}
	if skipped := len(csFiles) - parsed; skipped > 0 {
		log.Printf("[csharp-extractor] skipped %d generated file(s) of %d", skipped, len(csFiles))
	}

	allFacts = mergePartialTypes(allFacts)
	resolveCSharpTargets(allFacts)
	computeCSharpPerformsIO(allFacts)

	for dir := range modules {
		props := map[string]any{
			"language":           "csharp",
			facts.PropModuleRole: facts.ModuleRoleForPath(dir),
		}
		if name, ok := projectForDir(dir, projects); ok {
			props["project"] = name
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

// computeCSharpPerformsIO propagates the direct-I/O signal (io_direct, set by the
// walker on members that call a network/file/database primitive) transitively over
// the intra-repo call graph into a performs_io prop, so a member reaching I/O only
// through wrapper layers is still flagged and a per-iteration call to it reads as
// an N+1. Cycle-safe monotone fixpoint; mirrors computeRustPerformsIO.
func computeCSharpPerformsIO(allFacts []facts.Fact) {
	exists := make(map[string]bool)
	for i := range allFacts {
		if allFacts[i].Kind == facts.KindSymbol {
			exists[allFacts[i].Name] = true
		}
	}

	io := make(map[string]bool)
	adj := make(map[string][]string)
	for i := range allFacts {
		f := &allFacts[i]
		if f.Kind != facts.KindSymbol {
			continue
		}
		if b, _ := f.Props["io_direct"].(bool); b {
			io[f.Name] = true
		}
		seen := make(map[string]bool)
		for _, r := range f.Relations {
			if r.Kind != facts.RelCalls || r.Target == f.Name || seen[r.Target] || !exists[r.Target] {
				continue
			}
			seen[r.Target] = true
			adj[f.Name] = append(adj[f.Name], r.Target)
		}
	}

	for changed := true; changed; {
		changed = false
		for name, callees := range adj {
			if io[name] {
				continue
			}
			for _, c := range callees {
				if io[c] {
					io[name] = true
					changed = true
					break
				}
			}
		}
	}

	for i := range allFacts {
		f := &allFacts[i]
		if f.Kind == facts.KindSymbol && io[f.Name] {
			if f.Props == nil {
				f.Props = map[string]any{}
			}
			f.Props["performs_io"] = true
		}
	}
}
