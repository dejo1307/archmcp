package cppextractor

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/parallel"
)

// CppExtractor extracts architectural facts from C++ source code using
// tree-sitter AST parsing (see cpp_ast.go for the walker implementation).
type CppExtractor struct{}

// New creates a new CppExtractor.
func New() *CppExtractor {
	return &CppExtractor{}
}

func (e *CppExtractor) Name() string {
	return "cpp"
}

// Detect returns true if the repository looks like a C++ project.
//
// An unambiguous C++ source extension (.cpp/.cc/.cxx/.hpp/...) is decisive. A
// build file (CMakeLists.txt/Makefile/meson.build/*.vcxproj) plus any header is
// also accepted. A bare .h/.c alone is NOT a signal — that would false-positive on
// pure-C projects.
func (e *CppExtractor) Detect(repoPath string) (bool, error) {
	hasCppSource := false
	hasBuildFile := false
	hasHeader := false

	walkShallow(repoPath, 3, func(path string, isDir bool) {
		if isDir {
			return
		}
		name := filepath.Base(path)
		switch {
		case isUnambiguousCppExt(name):
			hasCppSource = true
		case isHeaderExt(name):
			hasHeader = true
		case name == "CMakeLists.txt" || name == "Makefile" ||
			name == "meson.build" || strings.HasSuffix(name, ".vcxproj"):
			hasBuildFile = true
		}
	})

	return hasCppSource || (hasBuildFile && hasHeader), nil
}

// Extract parses C++ files with tree-sitter and emits architectural facts.
//
// It mirrors the Swift extractor's structure:
//   - Pass 1 walks each file's AST (extractFileAST) to emit declaration, member,
//     include-dependency and call-graph facts, while building a type index
//     (simple type name -> module dir) and a header index (header basename -> dir).
//   - dedupeSymbols merges header method prototypes with their out-of-line source
//     definitions (the same canonical name resolves both; see cpp_ast.go).
//   - canonicalizeTargets rewrites bare call/inheritance/instantiation edge targets
//     to canonical "<dir>.<...>" fact names so reverse traversal connects dependents.
//   - resolveIncludeDependencies rewrites quoted #include targets to module dirs.
//   - Module facts are emitted per directory.
func (e *CppExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var allFacts []facts.Fact

	hFilesAreCpp := repoHasUnambiguousCpp(files)

	modules := make(map[string]bool)
	typeIndex := make(map[string]string)   // simple type name -> dir
	headerIndex := make(map[string]string) // header/source basename -> dir

	// Pass 1: AST extraction (parallel) + indices (rebuilt in file order).
	var cppFiles []string
	for _, relFile := range files {
		if isCppFile(relFile, hFilesAreCpp) {
			cppFiles = append(cppFiles, relFile)
		}
	}

	// extractFileAST is pure; parse in parallel. The type/header indices below are
	// then rebuilt by iterating the per-file results in file order, so they (and
	// their last-write-wins on duplicate names) are identical to a serial run.
	perFileFacts := parallel.MapFiles(ctx, cppFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[cpp-extractor] error reading %s: %v", relFile, err)
			return nil
		}
		return extractFileAST(src, relFile)
	})

	for i, fileFacts := range perFileFacts {
		relFile := cppFiles[i]
		allFacts = append(allFacts, fileFacts...)

		dir := filepath.Dir(relFile)
		modules[dir] = true
		headerIndex[filepath.Base(relFile)] = dir

		// Index declared types so edge targets resolve.
		for _, fact := range fileFacts {
			if fact.Kind != facts.KindSymbol {
				continue
			}
			sk, _ := fact.Props["symbol_kind"].(string)
			switch sk {
			case facts.SymbolClass, facts.SymbolStruct, facts.SymbolEnum, facts.SymbolInterface:
				if simple := lastScopeComponent(fact.Name); simple != "" {
					typeIndex[simple] = dir
				}
			}
		}
	}

	// Merge header method declarations with source definitions.
	allFacts = dedupeSymbols(allFacts)

	// Canonicalise bare edge targets to "<dir>.<...>".
	canonicalizeTargets(allFacts, typeIndex)

	// Resolve quoted #include targets to module dirs.
	resolveIncludeDependencies(allFacts, headerIndex)

	// Emit module facts per directory.
	for dir := range modules {
		allFacts = append(allFacts, facts.Fact{
			Kind: facts.KindModule,
			Name: dir,
			File: dir,
			Props: map[string]any{
				"language": "cpp",
			},
		})
	}

	return allFacts, nil
}

// dedupeSymbols merges duplicate symbol facts that share a canonical name — most
// importantly a class's header method prototype and its out-of-line source
// definition. The definition (has_body=true) is authoritative for File/Line and
// carries the call-graph relations; props and relations are unioned.
func dedupeSymbols(in []facts.Fact) []facts.Fact {
	byName := make(map[string]int)
	out := make([]facts.Fact, 0, len(in))
	for _, f := range in {
		if f.Kind != facts.KindSymbol {
			out = append(out, f)
			continue
		}
		if j, ok := byName[f.Name]; ok {
			mergeSymbol(&out[j], f)
			continue
		}
		byName[f.Name] = len(out)
		out = append(out, f)
	}
	return out
}

func mergeSymbol(dst *facts.Fact, src facts.Fact) {
	dstHasBody, _ := dst.Props["has_body"].(bool)
	srcHasBody, _ := src.Props["has_body"].(bool)

	// Prefer the definition's location.
	if srcHasBody && !dstHasBody {
		dst.File = src.File
		dst.Line = src.Line
	}

	// Union props, preferring truthy booleans and keeping a non-empty receiver.
	for k, v := range src.Props {
		switch existing := dst.Props[k]; existing {
		case nil:
			dst.Props[k] = v
		default:
			if b, ok := v.(bool); ok && b {
				dst.Props[k] = true
			}
			if k == "receiver" {
				if s, ok := v.(string); ok && s != "" {
					dst.Props[k] = s
				}
			}
		}
	}
	// A method identity wins over a plain function (out-of-line defs are methods).
	if skSrc, _ := src.Props["symbol_kind"].(string); skSrc == facts.SymbolMethod {
		dst.Props["symbol_kind"] = facts.SymbolMethod
	}

	// Union relations (dedup by kind+target).
	for _, r := range src.Relations {
		dup := false
		for _, e := range dst.Relations {
			if e.Kind == r.Kind && e.Target == r.Target {
				dup = true
				break
			}
		}
		if !dup {
			dst.Relations = append(dst.Relations, r)
		}
	}
}

// canonicalizeTargets rewrites bare simple-name / "Scope::name" targets of
// call-graph and inheritance relations to canonical "<dir>.<...>" fact names using
// the type index. Targets that already contain "." are left unchanged.
//
// Unresolved RelInstantiates edges are DROPPED: a bare capitalized callee that is
// not a known repo type is almost always a C-style macro or function (e.g.
// Malloc, List_Nbr), not a constructor — keeping them would flood the graph with
// false instantiation edges. RelImplements / RelCalls to unknown (external) names
// are kept, since cross-module/external inheritance and calls are meaningful.
func canonicalizeTargets(allFacts []facts.Fact, typeIndex map[string]string) {
	for i := range allFacts {
		rels := allFacts[i].Relations
		kept := rels[:0]
		for _, r := range rels {
			switch r.Kind {
			case facts.RelImplements, facts.RelDependsOn:
				if !strings.Contains(r.Target, ".") {
					if dir, ok := typeIndex[r.Target]; ok {
						r.Target = dir + "." + r.Target
					}
				}
			case facts.RelInstantiates:
				if !strings.Contains(r.Target, ".") {
					dir, ok := typeIndex[r.Target]
					if !ok {
						continue // drop: not a known type (macro / external function)
					}
					r.Target = dir + "." + r.Target
				}
			case facts.RelCalls:
				if !strings.Contains(r.Target, ".") {
					// "Scope::name" — resolve the scope (a class/namespace type) to its dir.
					if idx := strings.Index(r.Target, "::"); idx >= 0 {
						if dir, ok := typeIndex[r.Target[:idx]]; ok {
							r.Target = dir + "." + r.Target
						}
					}
				}
			}
			kept = append(kept, r)
		}
		allFacts[i].Relations = kept
	}
}

// resolveIncludeDependencies rewrites quoted #include dependency targets (bare
// header paths) to the module dir that declares the header. Includes that resolve
// to a different module are kept as inter-module edges; same-dir and unresolved
// (external/vendored/system) includes are marked accordingly.
func resolveIncludeDependencies(allFacts []facts.Fact, headerIndex map[string]string) {
	for i := range allFacts {
		f := &allFacts[i]
		if f.Kind != facts.KindDependency {
			continue
		}
		inc, _ := f.Props["include"].(string)
		if inc == "" {
			continue
		}
		base := filepath.Base(inc)
		if dir, ok := headerIndex[base]; ok {
			for j := range f.Relations {
				if f.Relations[j].Kind == facts.RelImports {
					f.Relations[j].Target = dir
				}
			}
			f.Props["source"] = "internal"
		} else {
			f.Props["source"] = "external"
		}
	}
}

// lastScopeComponent returns the final "::"-separated component of a "<dir>.<...>"
// fact name (e.g. "Geo.gmsh::Base" -> "Base").
func lastScopeComponent(name string) string {
	// Drop the "<dir>." prefix.
	if idx := strings.Index(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	if idx := strings.LastIndex(name, "::"); idx >= 0 {
		return name[idx+2:]
	}
	return name
}

// --- file extension helpers ---

// isUnambiguousCppExt reports whether a filename has a C++-only source extension.
func isUnambiguousCppExt(name string) bool {
	if filepath.Ext(name) == ".C" { // ".C" (capital) is C++ by convention
		return true
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".cpp", ".cc", ".cxx", ".c++", ".hpp", ".hxx", ".hh", ".h++", ".inl", ".ipp", ".tpp":
		return true
	}
	return false
}

// isHeaderExt reports whether a filename is a (possibly C) header.
func isHeaderExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".h", ".hpp", ".hxx", ".hh", ".h++", ".inl", ".ipp", ".tpp":
		return true
	}
	return false
}

// isCFile reports whether a filename is a pure-C source (.c), which the C++
// extractor skips.
func isCFile(name string) bool {
	return strings.ToLower(filepath.Ext(name)) == ".c"
}

// isCppFile reports whether the extractor should parse a file. Bare .h files are
// parsed only when the repo contains unambiguous C++ sources (hFilesAreCpp).
func isCppFile(path string, hFilesAreCpp bool) bool {
	if isCFile(path) {
		return false
	}
	if isUnambiguousCppExt(path) {
		return true
	}
	if strings.ToLower(filepath.Ext(path)) == ".h" {
		return hFilesAreCpp
	}
	return false
}

// OwnsFile implements plugin.FileOwner for incremental caching. It owns a
// superset of what isCppFile parses (bare .h files are always claimed, since the
// repo-wide hFilesAreCpp decision is not available per-file); over-claiming only
// narrows what counts as shared config and never under-invalidates the cache.
func (e *CppExtractor) OwnsFile(relFile string) bool {
	if isUnambiguousCppExt(relFile) {
		return true
	}
	return strings.ToLower(filepath.Ext(relFile)) == ".h"
}

// repoHasUnambiguousCpp reports whether any file in the list has a C++-only
// source extension (used to decide whether bare .h files are C++).
func repoHasUnambiguousCpp(files []string) bool {
	for _, f := range files {
		if isUnambiguousCppExt(f) {
			return true
		}
	}
	return false
}

// walkShallow invokes fn for each entry up to maxDepth directory levels below
// root (root entries are depth 1). It is best-effort and ignores read errors.
func walkShallow(root string, maxDepth int, fn func(path string, isDir bool)) {
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			full := filepath.Join(dir, name)
			fn(full, entry.IsDir())
			if entry.IsDir() && depth < maxDepth {
				walk(full, depth+1)
			}
		}
	}
	walk(root, 1)
}
