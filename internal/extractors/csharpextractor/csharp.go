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
	"sort"
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
// solution or project file of ANY .NET language, or any real C# source, within
// detectMaxDepth levels.
//
// A project file alone is not required, mirroring the Java extractor's reasoning
// in reverse: `global.json` and `Directory.Build.props` are .NET-only but appear
// in repositories that vendor a .NET tool without containing C#, while a `.cs`
// file is unambiguous — no other ecosystem uses that extension.
//
// Detecting on `.fsproj` and `.vbproj` is what stops a claimed repository from
// being an empty one. giraffe-fsharp/Giraffe ships `Giraffe.slnx`, 7 `.fsproj`
// and no `.cs` at all: this extractor matched the solution, claimed the
// repository, emitted zero facts and reported success — a green snapshot of 46
// unread sources, indistinguishable from an empty repo. Now the project graph is
// read whatever language the sources are in, so a claim always produces facts.
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
		if isCSharpFile(path) || isProjectFile(path) || isSolutionFile(path) ||
			isRazorFile(path) || isXamlFile(path) || isVBFile(path) {
			found = true
		}
		return nil
	})
	return found, nil
}

func isCSharpFile(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".cs")
}

// isProjectFile and isSolutionFile live in msbuild.go, which owns every MSBuild
// format this extractor reads.

// OwnsFile implements plugin.FileOwner for incremental caching. Project and
// solution files are owned alongside sources because they seed the project index
// and the assembly graph every module fact reads, so adding, renaming or editing
// one must invalidate the cache — editing a ProjectReference changes the graph
// without touching a single .cs file.
func (e *CSharpExtractor) OwnsFile(relFile string) bool {
	return isCSharpFile(relFile) || isProjectFile(relFile) || isSolutionFile(relFile) ||
		isRazorFile(relFile) || isXamlFile(relFile) || isVBFile(relFile)
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
	var csFiles, projFiles, slnFiles, razorFiles, xamlFiles, vbFiles []string
	for _, relFile := range files {
		switch {
		case isCSharpFile(relFile):
			csFiles = append(csFiles, relFile)
		case isProjectFile(relFile):
			projFiles = append(projFiles, relFile)
		case isSolutionFile(relFile):
			slnFiles = append(slnFiles, relFile)
		case isRazorFile(relFile):
			razorFiles = append(razorFiles, relFile)
		case isXamlFile(relFile):
			xamlFiles = append(xamlFiles, relFile)
		case isVBFile(relFile):
			vbFiles = append(vbFiles, relFile)
		}
	}

	projects := buildProjectIndex(projFiles)
	msbuildProjects := parseProjects(repoPath, projFiles, slnFiles)

	perFileFacts := parallel.MapFiles(ctx, csFiles, func(relFile string) fileResult {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[csharp-extractor] error reading %s: %v", relFile, err)
			return fileResult{}
		}
		if isGeneratedSource(relFile, src) {
			return fileResult{generated: true}
		}
		ff, sc := extractFileASTFull(src, relFile)
		return fileResult{facts: ff, scaffold: sc}
	})

	var allFacts []facts.Fact
	var scaffold aspnetScaffold
	modules := make(map[string]bool)
	parsed := 0
	for i, r := range perFileFacts {
		if r.generated {
			continue
		}
		parsed++
		allFacts = append(allFacts, r.facts...)
		scaffold.merge(r.scaffold)
		modules[filepath.ToSlash(filepath.Dir(csFiles[i]))] = true
	}
	if skipped := len(csFiles) - parsed; skipped > 0 {
		log.Printf("[csharp-extractor] skipped %d generated file(s) of %d", skipped, len(csFiles))
	}

	// Razor files are walked before the partial merge, because a `.razor`
	// component and its `.razor.cs` code-behind are two halves of one generated
	// class and must converge into a single fact.
	razorResults := parallel.MapFiles(ctx, razorFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[csharp-extractor] error reading %s: %v", relFile, err)
			return nil
		}
		return razorFacts(string(src), relFile)
	})
	for i, ff := range razorResults {
		allFacts = append(allFacts, ff...)
		modules[filepath.ToSlash(filepath.Dir(razorFiles[i]))] = true
	}
	if len(razorFiles) > 0 {
		log.Printf("[csharp-extractor] parsed %d Razor file(s)", len(razorFiles))
	}

	// XAML, for the same reason and before the same merge: an x:Class document is
	// one half of a partial class whose other half is its .xaml.cs code-behind.
	xamlResults := parallel.MapFiles(ctx, xamlFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[csharp-extractor] error reading %s: %v", relFile, err)
			return nil
		}
		return xamlFacts(string(src), relFile)
	})
	for i, ff := range xamlResults {
		allFacts = append(allFacts, ff...)
		modules[filepath.ToSlash(filepath.Dir(xamlFiles[i]))] = true
	}
	if len(xamlFiles) > 0 {
		log.Printf("[csharp-extractor] parsed %d XAML file(s)", len(xamlFiles))
	}

	// VB.NET. Emitted into the SAME fact set as C#, which is the point: a solution
	// mixes the two languages inside one assembly, so a VB class referencing a C#
	// type must resolve through the one shared type index resolveCSharpTargets
	// builds below.
	vbResults := parallel.MapFiles(ctx, vbFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[csharp-extractor] error reading %s: %v", relFile, err)
			return nil
		}
		if isGeneratedVB(relFile, string(src)) {
			return nil
		}
		return scanVB(string(src), relFile)
	})
	vbParsed := 0
	for i, ff := range vbResults {
		if ff == nil {
			continue
		}
		vbParsed++
		allFacts = append(allFacts, ff...)
		modules[filepath.ToSlash(filepath.Dir(vbFiles[i]))] = true
	}
	if len(vbFiles) > 0 {
		log.Printf("[csharp-extractor] parsed %d VB.NET file(s) of %d", vbParsed, len(vbFiles))
	}

	allFacts = mergePartialTypes(allFacts)
	resolveCSharpTargets(allFacts)
	// After resolution: composeControllerRoutes walks the inheritance edges to find
	// an inherited [Route], and those targets are bare type names until resolution
	// canonicalises them.
	allFacts = append(allFacts, composeControllerRoutes(allFacts, scaffold)...)
	computeCSharpPerformsIO(allFacts)

	// Module facts are built into a map keyed by directory, then overlaid with the
	// MSBuild assembly graph, so a project root that also holds sources yields one
	// fact rather than two facts the graph would have to merge.
	mods := make(map[string]*facts.Fact, len(modules)+len(msbuildProjects))
	for dir := range modules {
		props := map[string]any{
			"language":           "csharp",
			facts.PropModuleRole: facts.ModuleRoleForPath(dir),
		}
		if name, ok := projectForDir(dir, projects); ok {
			props["project"] = name
		}
		mods[dir] = &facts.Fact{
			Kind:  facts.KindModule,
			Name:  dir,
			File:  dir,
			Props: props,
		}
	}
	nugetDeps := applyMSBuild(mods, msbuildProjects)

	dirs := make([]string, 0, len(mods))
	for dir := range mods {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		allFacts = append(allFacts, *mods[dir])
	}
	allFacts = append(allFacts, nugetDeps...)

	// len(csFiles), not `parsed`: a repository whose every .cs file was skipped as
	// generated is a different situation, and the generated-skip count already
	// reports it above.
	if len(csFiles) == 0 && len(msbuildProjects) > 0 {
		// The Giraffe case: projects found, no readable source. Say so rather than
		// reporting a successful snapshot of nothing — the assembly graph below is
		// real, but it is not the whole repository.
		langs := map[string]int{}
		for _, p := range msbuildProjects {
			langs[p.language]++
		}
		log.Printf("[csharp-extractor] no C# sources: emitting the project graph only "+
			"(%d project(s) by language %v); sources in other .NET languages are not read yet",
			len(msbuildProjects), langs)
	}

	return allFacts, nil
}

// fileResult holds one file's extracted facts plus its ASP.NET routing evidence,
// returned together from the parallel per-file walk. `generated` distinguishes a
// file that was deliberately skipped from one that merely produced nothing, so the
// skip count stays honest.
type fileResult struct {
	facts     []facts.Fact
	scaffold  aspnetScaffold
	generated bool
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
