package cppextractor

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/factpath"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/parallel"
)

// Language identifiers emitted as the per-fact "language" prop. This extractor is
// a C/C++ family extractor: .c (and C-context .h) files are parsed with the
// tree-sitter-c grammar, while .cpp/.cc/... (and C++-context .h) use tree-sitter-cpp.
const (
	langC   = "c"
	langCpp = "cpp"
)

// relFuncPtrCandidate is an internal, provisional relation kind for an identifier
// used as a function pointer (in an initializer value or a call argument). It is
// resolved by resolveFuncPtrRefs in Extract — rewritten to facts.RelCalls when the
// target names a real function in the snapshot, otherwise dropped — so it never
// appears in emitted facts.
const relFuncPtrCandidate = "func_ptr_candidate"

// CppExtractor extracts architectural facts from C and C++ source code using
// tree-sitter AST parsing (see cpp_ast.go for the walker implementation). It owns
// both languages: a per-file grammar choice (see parsedLanguage) routes .c files
// to tree-sitter-c and .cpp/... to tree-sitter-cpp, and every fact carries a
// "language" prop ("c" or "cpp") so the two are distinguishable downstream.
type CppExtractor struct{}

// New creates a new CppExtractor.
func New() *CppExtractor {
	return &CppExtractor{}
}

func (e *CppExtractor) Name() string {
	return "cpp"
}

// Detect returns true if the repository looks like a C or C++ project.
//
// The extractor handles both languages, so a pure-C source (.c) or an
// unambiguous C++ source extension (.cpp/.cc/.cxx/.hpp/...) is decisive. A build
// file (CMakeLists.txt/Makefile/meson.build/*.vcxproj) plus any header is also
// accepted. A bare .h alone is NOT a signal — that would false-positive on repos
// that merely vendor a header.
func (e *CppExtractor) Detect(repoPath string) (bool, error) {
	hasCSource := false
	hasCppSource := false
	hasBuildFile := false
	hasHeader := false

	walkShallow(repoPath, 3, func(path string, isDir bool) {
		if isDir {
			return
		}
		name := filepath.Base(path)
		switch {
		case isCFile(name):
			hasCSource = true
		case isUnambiguousCppExt(name):
			hasCppSource = true
		case isHeaderExt(name):
			hasHeader = true
		case name == "CMakeLists.txt" || name == "Makefile" ||
			name == "meson.build" || strings.HasSuffix(name, ".vcxproj"):
			hasBuildFile = true
		}
	})

	return hasCSource || hasCppSource || (hasBuildFile && hasHeader), nil
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

	// Per-directory grammar attribution for bare .h headers: a header is C++ only
	// when its own directory subtree contains C++ sources. This localises a handful
	// of stray .cpp files (e.g. tools/ in a kernel tree) instead of flipping every
	// .h in the repo to the C++ grammar.
	hdrLang := buildHeaderLangIndex(files)

	modules := make(map[string]bool)
	dirLang := make(map[string]string)              // dir -> module language ("c"/"cpp")
	typeIndex := make(map[string]string)            // simple type name -> dir
	headerIndex := make(map[string]string)          // header/source basename -> dir
	funcNames := make(map[string]bool)              // short names of all functions/methods
	moduleRefs := make(map[string][]facts.Relation) // dir -> file-scope macro-call refs

	// Pass 1: AST extraction (parallel) + indices (rebuilt in file order).
	var cppFiles []string
	langByFile := make(map[string]string) // rel path -> "c"/"cpp"
	for _, relFile := range files {
		if lang := parsedLanguage(relFile, hdrLang); lang != "" {
			cppFiles = append(cppFiles, relFile)
			langByFile[relFile] = lang
		}
	}

	// Pre-pass: build a project-wide #define table (parallel scan), so a file-scope
	// macro invocation (CONFIGFS_ATTR, DEVICE_ATTR_RO, …) can be expanded using the
	// definition that lives in a header, recovering the token-pasted callbacks it
	// references. Merged in file order for deterministic last-write-wins.
	perFileMacros := parallel.MapFiles(ctx, cppFiles, func(relFile string) macroTable {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			return nil
		}
		t := macroTable{}
		collectMacros(src, t)
		return t
	})
	macros := macroTable{}
	for _, t := range perFileMacros {
		for name, def := range t {
			macros[name] = def
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
		return extractFileAST(src, relFile, langByFile[relFile], macros)
	})

	for i, fileFacts := range perFileFacts {
		relFile := cppFiles[i]
		dir := factpath.Dir(relFile)

		for _, f := range fileFacts {
			// The walker emits a KindModule fact only to host file-scope macro-call
			// references; fold those into the per-dir set and emit one canonical
			// module fact per directory below.
			if f.Kind == facts.KindModule {
				moduleRefs[dir] = append(moduleRefs[dir], f.Relations...)
				continue
			}
			allFacts = append(allFacts, f)
		}

		modules[dir] = true
		headerIndex[filepath.Base(relFile)] = dir
		// A directory mixing C and C++ sources is treated as a C++ module; a
		// C-only directory stays "c".
		if langByFile[relFile] == langCpp {
			dirLang[dir] = langCpp
		} else if dirLang[dir] == "" {
			dirLang[dir] = langC
		}

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
			case facts.SymbolFunc, facts.SymbolMethod:
				if simple := lastScopeComponent(fact.Name); simple != "" {
					funcNames[simple] = true
				}
			}
		}
	}

	// Emit module facts per directory, before func-pointer resolution so the
	// file-scope registration-macro references collected on them (module_init(foo),
	// EXPORT_SYMBOL(foo)) resolve and canonicalise alongside ordinary edges.
	for dir := range modules {
		lang := dirLang[dir]
		if lang == "" {
			lang = langC
		}
		allFacts = append(allFacts, facts.Fact{
			Kind:      facts.KindModule,
			Name:      dir,
			File:      dir,
			Props:     map[string]any{"language": lang},
			Relations: moduleRefs[dir],
		})
	}

	// Resolve provisional func-pointer references (initializer values, call
	// arguments, and file-scope macro-call arguments): keep only those that name a
	// real function, rewriting them to call edges; drop the rest so ordinary data
	// identifiers create no edge.
	resolveFuncPtrRefs(allFacts, funcNames)

	// Merge header method declarations with source definitions.
	allFacts = dedupeSymbols(allFacts)

	// Canonicalise bare edge targets to "<dir>.<...>".
	canonicalizeTargets(allFacts, typeIndex)

	// Propagate the direct-I/O signal transitively over the (now-canonical) call
	// graph, so a function reaching a file/socket primitive through a wrapper is
	// also performs_io — lets the performance analyzer flag an in-loop wrapper call
	// as an N+1.
	computeCppPerformsIO(allFacts)

	// Resolve quoted #include targets to module dirs.
	resolveIncludeDependencies(allFacts, headerIndex)

	return allFacts, nil
}

// computeCppPerformsIO seeds performs_io from io_direct and propagates it over
// RelCalls edges whose target is a known symbol Name, via a monotone false->true
// fixpoint (a port of the Python/Rust/PHP extractors' pass). Call targets are
// canonical "<dir>.<name>" after canonicalizeTargets, matching symbol Names, so a
// same- or cross-directory resolved call propagates the flag.
func computeCppPerformsIO(allFacts []facts.Fact) {
	exists := make(map[string]bool)
	for i := range allFacts {
		if allFacts[i].Kind == facts.KindSymbol {
			exists[allFacts[i].Name] = true
		}
	}

	io := make(map[string]bool)      // name -> performs I/O (directly or transitively)
	adj := make(map[string][]string) // name -> called names that are known symbols
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

// dedupeSymbols merges duplicate symbol facts that share a canonical name — most
// importantly a class's header method prototype and its out-of-line source
// definition. The definition (has_body=true) is authoritative for File/Line and
// carries the call-graph relations; props and relations are unioned.
// resolveFuncPtrRefs finalizes the provisional relFuncPtrCandidate edges emitted
// for identifiers used as function pointers (initializer values, call arguments).
// An edge is kept — rewritten to facts.RelCalls — only when its target's short
// name is an actual function/method in the snapshot; otherwise it is dropped, so a
// data identifier that merely shares a name space with nothing real adds no edge.
// Duplicates of an edge already present on the fact are collapsed.
func resolveFuncPtrRefs(allFacts []facts.Fact, funcNames map[string]bool) {
	for i := range allFacts {
		rels := allFacts[i].Relations
		if len(rels) == 0 {
			continue
		}
		seen := make(map[string]bool, len(rels))
		for _, r := range rels {
			if r.Kind != relFuncPtrCandidate {
				seen[r.Kind+"\x00"+r.Target] = true
			}
		}
		out := rels[:0]
		for _, r := range rels {
			if r.Kind == relFuncPtrCandidate {
				if !funcNames[lastScopeComponent(r.Target)] {
					continue // not a real function — drop the provisional edge
				}
				r.Kind = facts.RelCalls
				if seen[r.Kind+"\x00"+r.Target] {
					continue // already a real call edge to the same target
				}
				seen[r.Kind+"\x00"+r.Target] = true
			}
			out = append(out, r)
		}
		allFacts[i].Relations = out
	}
}

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

// isCFile reports whether a filename is a pure-C source (.c).
func isCFile(name string) bool {
	return strings.ToLower(filepath.Ext(name)) == ".c"
}

// parsedLanguage reports which grammar should parse a file, or "" if the file is
// not a C/C++ source the extractor handles. hdrLang maps a directory to the
// language attributed to its bare .h headers (see buildHeaderLangIndex).
func parsedLanguage(path string, hdrLang map[string]string) string {
	if isCFile(path) {
		return langC
	}
	if isUnambiguousCppExt(path) {
		return langCpp
	}
	if strings.ToLower(filepath.Ext(path)) == ".h" {
		if l := hdrLang[factpath.Dir(path)]; l != "" {
			return l
		}
		return langC // default: bare .h with no nearby C++ sources is C
	}
	return ""
}

// buildHeaderLangIndex attributes a language to the bare .h headers in each
// directory. A header is C++ only when its own directory subtree contains
// unambiguous C++ sources; otherwise it is C. This localises stray C++ files
// (e.g. a few tools/*.cpp in an otherwise pure-C kernel tree) to their own
// subtrees instead of flipping every .h in the repo to the C++ grammar.
//
// The returned map holds langCpp for directories whose subtree contains C++
// sources; directories absent from the map default to langC at lookup time.
func buildHeaderLangIndex(files []string) map[string]string {
	// Directories that directly contain an unambiguous C++ source.
	cppDirs := make(map[string]bool)
	for _, f := range files {
		if isUnambiguousCppExt(f) {
			cppDirs[factpath.Dir(f)] = true
		}
	}
	// A header's directory is C++ if it, or any ancestor directory, directly
	// contains C++ sources (covers a src/ tree with headers in subdirs).
	hdrLang := make(map[string]string)
	for _, f := range files {
		if strings.ToLower(filepath.Ext(f)) != ".h" {
			continue
		}
		dir := factpath.Dir(f)
		if _, done := hdrLang[dir]; done {
			continue
		}
		for d := dir; ; d = factpath.Dir(d) {
			if cppDirs[d] {
				hdrLang[dir] = langCpp
				break
			}
			if d == "." || d == "/" || d == factpath.Dir(d) {
				break
			}
		}
	}
	return hdrLang
}

// OwnsFile implements plugin.FileOwner for incremental caching. It owns every
// file the extractor may parse — C and C++ sources plus bare .h headers (the
// per-directory header-language decision is not available per-file, so .h is
// always claimed). Over-claiming only narrows what counts as shared config and
// never under-invalidates the cache.
func (e *CppExtractor) OwnsFile(relFile string) bool {
	if isUnambiguousCppExt(relFile) || isCFile(relFile) {
		return true
	}
	return strings.ToLower(filepath.Ext(relFile)) == ".h"
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
