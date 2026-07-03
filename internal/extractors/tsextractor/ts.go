package tsextractor

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/parallel"

	sitter "github.com/tree-sitter/go-tree-sitter"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// TSExtractor extracts architectural facts from TypeScript/TSX source code using tree-sitter.
type TSExtractor struct{}

// New creates a new TSExtractor.
func New() *TSExtractor {
	return &TSExtractor{}
}

func (e *TSExtractor) Name() string {
	return "typescript"
}

// Detect returns true if the repository (or one of its immediate subdirectories
// in the case of a monorepo) contains TypeScript markers.
func (e *TSExtractor) Detect(repoPath string) (bool, error) {
	_, found := findTSRoot(repoPath)
	return found, nil
}

// findTSRoot returns the directory that is the TypeScript project root, along
// with a boolean indicating whether one was found. Search depth adapts to
// repo structure: Java/Gradle projects nest UI code deep (src/main/resources/ui)
// so we search up to 8 levels; plain repos need at most 2.
func findTSRoot(repoPath string) (string, bool) {
	if hasTSMarkers(repoPath) {
		return repoPath, true
	}
	maxDepth := 2
	if isDeepNestedProject(repoPath) {
		maxDepth = 8
	}
	return searchTSRoot(repoPath, 0, maxDepth)
}

func isDeepNestedProject(repoPath string) bool {
	markers := []string{
		"pom.xml", "build.gradle", "build.gradle.kts",
		"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt",
	}
	for _, marker := range markers {
		if _, err := os.Stat(filepath.Join(repoPath, marker)); err == nil {
			return true
		}
	}
	return false
}

var tsSkipDirs = map[string]bool{
	"node_modules": true, "dist": true, ".next": true,
	"build": true, "out": true, "target": true, "vendor": true,
}

func searchTSRoot(dir string, depth, maxDepth int) (string, bool) {
	if depth >= maxDepth {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || tsSkipDirs[entry.Name()] {
			continue
		}
		sub := filepath.Join(dir, entry.Name())
		if hasTSMarkers(sub) {
			return sub, true
		}
		if found, ok := searchTSRoot(sub, depth+1, maxDepth); ok {
			return found, true
		}
	}
	return "", false
}

// hasTSMarkers returns true if the directory looks like a project root this
// extractor should handle (TypeScript, or a JS framework it also parses).
func hasTSMarkers(dir string) bool {
	// tsconfig.json (standard) or tsconfig.base.json (Nx monorepo)
	for _, name := range []string{"tsconfig.json", "tsconfig.base.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}

	for _, pkg := range []string{"typescript", "vue", "react", "svelte", "next", "nuxt"} {
		if hasPkgDependency(dir, pkg) {
			return true
		}
	}
	return false
}

// Extract parses TypeScript/TSX files and emits architectural facts.
func (e *TSExtractor) Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error) {
	var allFacts []facts.Fact

	// Detect frameworks
	isNextJS := detectNextJS(repoPath)
	isVue := detectVue(repoPath)
	isNuxt := detectNuxt(repoPath)
	isSvelteKit := detectSvelteKit(repoPath)

	// Parse tsconfig.json path aliases, one root per package for monorepos.
	aliasRoots := collectTSAliasRoots(repoPath)

	// SvelteKit maps $lib → src/lib by convention.
	if isSvelteKit {
		aliasRoots = withSvelteKitLibDefault(aliasRoots)
	}

	// Restrict to TypeScript files once, then parse them in parallel. The
	// framework flags and path aliases above are read-only, and extractFile is a
	// pure function of (src, relFile, …), so per-file work is independent. Results
	// are merged in file order for deterministic output.
	var tsFiles []string
	knownFiles := make(map[string]bool)
	for _, relFile := range files {
		if isTypeScriptFile(relFile) {
			tsFiles = append(tsFiles, relFile)
			knownFiles[filepath.ToSlash(relFile)] = true
		}
	}

	perFileFacts := parallel.MapFiles(ctx, tsFiles, func(relFile string) []facts.Fact {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[ts-extractor] error reading %s: %v", relFile, err)
			return nil
		}
		aliases := aliasesForDir(aliasRoots, filepath.Dir(relFile))
		return e.extractFile(src, relFile, isNextJS, isVue, isNuxt, isSvelteKit, aliases, knownFiles)
	})

	// Group files by directory for module detection
	modules := make(map[string]bool)
	for i, fileFacts := range perFileFacts {
		allFacts = append(allFacts, fileFacts...)
		modules[filepath.Dir(tsFiles[i])] = true
	}

	// Emit module facts for each directory
	for dir := range modules {
		allFacts = append(allFacts, facts.Fact{
			Kind: facts.KindModule,
			Name: dir,
			File: dir,
			Props: map[string]any{
				"language": "typescript",
			},
		})
	}

	return allFacts, nil
}

// extractCtx bundles the per-file state threaded through declaration extraction
// so symbols can be enriched with React/Next.js semantic classification.
type extractCtx struct {
	src        []byte
	relFile    string
	dir        string
	isTSX      bool
	isNextJS   bool
	isVue      bool
	isNuxt     bool
	importMap  map[string]string
	knownFiles map[string]bool // repo-relative (slash) paths of all indexed TS/JS files
}

func (e *TSExtractor) extractFile(src []byte, relFile string, isNextJS, isVue, isNuxt, isSvelteKit bool, aliases map[string]string, knownFiles map[string]bool) []facts.Fact {
	if isVueFile(relFile) {
		return e.extractVueSFC(src, relFile, isNuxt, aliases)
	}
	if isSvelteFile(relFile) {
		return e.extractSvelteSFC(src, relFile, isSvelteKit, aliases)
	}

	var result []facts.Fact

	// Parse openapi-typescript generated files for backend API route dependencies.
	// These are client-role route facts showing which backend routes the TS code calls.
	if openapiRoutes := extractOpenAPITypescriptFacts(src, relFile); len(openapiRoutes) > 0 {
		result = append(result, openapiRoutes...)
	}

	// Hand-written fetch / makeRequest API calls are also client-role routes.
	result = append(result, extractHTTPClientFacts(src, relFile)...)

	isTSX := strings.HasSuffix(relFile, ".tsx") || strings.HasSuffix(relFile, ".jsx")
	lang := typescript.LanguageTypescript()
	if isTSX {
		lang = typescript.LanguageTSX()
	}

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(lang)); err != nil {
		return result
	}

	tree := parser.Parse(src, nil)
	defer tree.Close()

	root := tree.RootNode()

	// Extract from the tree
	result = append(result, e.extractImports(root, src, relFile, aliases)...)

	ctx := &extractCtx{
		src:        src,
		relFile:    relFile,
		dir:        filepath.Dir(relFile),
		isTSX:      isTSX,
		isNextJS:   isNextJS,
		isVue:      isVue,
		isNuxt:     isNuxt,
		importMap:  buildImportSymbols(root, src, relFile, aliases),
		knownFiles: knownFiles,
	}
	decls := e.extractDeclarations(root, ctx)

	// A declaration may be exported via a separate `export { A, B }` clause or
	// `export default Name` statement rather than an inline `export` keyword.
	// Mark the corresponding symbols as exported.
	if exported := collectExportedLocalNames(root, src); len(exported) > 0 {
		for i := range decls {
			if decls[i].Kind != facts.KindSymbol {
				continue
			}
			local := decls[i].Name[strings.LastIndexByte(decls[i].Name, '.')+1:]
			if exported[local] {
				decls[i].Props["exported"] = true
			}
		}
	}
	result = append(result, decls...)

	// Whole-file reference pass (JSX component rendering, imported-identifier values
	// like route configs, namespace member access, require()-bound names). Emitted as
	// a KindFileRef so file-scope references the per-function call walk cannot see do
	// not leave used code mis-reported as dead.
	result = append(result, e.collectTSFileRefs(root, ctx, aliases)...)

	// Detect Next.js routes
	if isNextJS {
		if routeFact := detectRoute(relFile); routeFact != nil {
			result = append(result, *routeFact)
		}
	}

	// Detect Vue Router configuration files
	if (isVue || isNuxt) && containsCreateRouterCall(root, src) {
		result = append(result, facts.Fact{
			Kind: facts.KindRoute,
			Name: relFile,
			File: relFile,
			Line: 1,
			Props: map[string]any{
				"type":      "router_config",
				"language":  "typescript",
				"framework": "vue",
			},
		})
	}

	return result
}

func (e *TSExtractor) extractImports(root *sitter.Node, src []byte, relFile string, aliases map[string]string) []facts.Fact {
	var result []facts.Fact
	dir := filepath.Dir(relFile)

	for i := range root.ChildCount() {
		child := root.Child(i)

		// export_statement only has a "source" field for re-exports
		// (export * from / export { X } from), not local declarations.
		var source *sitter.Node
		isReexport := false
		switch child.Kind() {
		case "import_statement":
			source = findChildByKind(child, "string")
		case "export_statement":
			source = child.ChildByFieldName("source")
			isReexport = true
		default:
			continue
		}
		if source == nil {
			continue
		}

		importPath := strings.Trim(nodeText(source, src), `"'`)

		// Resolve path aliases and relative imports to filesystem-relative paths
		resolved, isExternal := resolveImportPath(importPath, dir, aliases)

		importSource := "internal"
		if isExternal {
			importSource = "external"
		}

		props := map[string]any{
			"language": "typescript",
			"source":   importSource,
		}
		if isReexport {
			props["reexport"] = true
		}

		result = append(result, facts.Fact{
			Kind:  facts.KindDependency,
			Name:  dir + " -> " + resolved,
			File:  relFile,
			Line:  int(child.StartPosition().Row) + 1,
			Props: props,
			Relations: []facts.Relation{
				{Kind: facts.RelImports, Target: resolved},
			},
		})
	}

	// CommonJS require() and dynamic import() calls are the only import mechanism in
	// server/build/task trees and code-split call sites; capture them as dependency
	// edges too so those graphs are not invisible. These calls can be nested anywhere,
	// so walk the whole tree; a dir-pair is deduped against the static imports above.
	seenDep := make(map[string]bool)
	for _, r := range result {
		seenDep[r.Name] = true
	}
	var walkDeps func(n *sitter.Node)
	walkDeps = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Kind() == "call_expression" {
			if fn := n.ChildByFieldName("function"); fn != nil {
				isRequire := fn.Kind() == "identifier" && nodeText(fn, src) == "require"
				isDynImport := fn.Kind() == "import"
				if isRequire || isDynImport {
					if strArg := findChildByKind(n.ChildByFieldName("arguments"), "string"); strArg != nil {
						importPath := strings.Trim(nodeText(strArg, src), `"'`)
						resolved, isExternal := resolveImportPath(importPath, dir, aliases)
						name := dir + " -> " + resolved
						if !seenDep[name] {
							seenDep[name] = true
							source := "internal"
							if isExternal {
								source = "external"
							}
							result = append(result, facts.Fact{
								Kind:      facts.KindDependency,
								Name:      name,
								File:      relFile,
								Line:      int(n.StartPosition().Row) + 1,
								Props:     map[string]any{"language": "typescript", "source": source, "dynamic": true},
								Relations: []facts.Relation{{Kind: facts.RelImports, Target: resolved}},
							})
						}
					}
				}
			}
		}
		for i := range n.ChildCount() {
			walkDeps(n.Child(i))
		}
	}
	walkDeps(root)

	return result
}

func (e *TSExtractor) extractDeclarations(root *sitter.Node, ctx *extractCtx) []facts.Fact {
	var result []facts.Fact
	for i := range root.ChildCount() {
		result = append(result, e.extractNode(root.Child(i), ctx, false, "")...)
	}
	return result
}

// extractNode emits facts for a single declaration node. fallbackName supplies a
// name for anonymous default-exported declarations (e.g. `export default function
// () {}`), derived from the file name; it is ignored when the declaration has its
// own name.
func (e *TSExtractor) extractNode(node *sitter.Node, ctx *extractCtx, isExported bool, fallbackName string) []facts.Fact {
	var result []facts.Fact
	src, dir, relFile := ctx.src, ctx.dir, ctx.relFile

	switch node.Kind() {
	case "export_statement":
		isDefault := hasChildKind(node, "default")
		fb := ""
		if isDefault {
			fb = fileSymbolName(relFile)
		}
		// Named/inline declaration inside the export.
		if decl := firstDeclChild(node); decl != nil {
			return e.extractNode(decl, ctx, true, fb)
		}
		// Anonymous default export of a value: name it after the file.
		if isDefault {
			for _, k := range []string{"function_expression", "generator_function", "class", "arrow_function", "call_expression"} {
				if c := findChildByKind(node, k); c != nil {
					return e.extractNode(c, ctx, true, fb)
				}
			}
		}

	case "function_declaration", "function_expression", "generator_function_declaration", "generator_function":
		name := findChildByKind(node, "identifier")
		symbolName := fallbackName
		if name != nil {
			symbolName = nodeText(name, src)
		}
		if symbolName == "" {
			break
		}
		result = append(result, e.funcSymbol(node, node, ctx, symbolName, isExported))

	case "arrow_function":
		if fallbackName != "" {
			result = append(result, e.funcSymbol(node, node, ctx, fallbackName, isExported))
		}

	case "call_expression":
		// Reached for `export default memo(...)` / `forwardRef(...)`.
		if fallbackName != "" {
			result = append(result, e.funcSymbol(node, node, ctx, fallbackName, isExported))
		}

	case "class_declaration", "abstract_class_declaration", "class":
		name := findChildByKind(node, "type_identifier")
		symbolName := fallbackName
		if name != nil {
			symbolName = nodeText(name, src)
		}
		if symbolName == "" {
			break
		}
		f := facts.Fact{
			Kind: facts.KindSymbol,
			Name: dir + "." + symbolName,
			File: relFile,
			Line: int(node.StartPosition().Row) + 1,
			Props: map[string]any{
				"symbol_kind": facts.SymbolClass,
				"exported":    isExported,
				"language":    "typescript",
			},
			Relations: []facts.Relation{
				{Kind: facts.RelDeclares, Target: dir},
			},
		}

		// Check for implements clause (nested under class_heritage)
		for j := range node.ChildCount() {
			c := node.Child(j)
			if c.Kind() == "class_heritage" {
				for k := range c.ChildCount() {
					heritage := c.Child(k)
					if heritage.Kind() == "implements_clause" {
						for l := range heritage.ChildCount() {
							t := heritage.Child(l)
							if t.Kind() == "type_identifier" {
								f.Relations = append(f.Relations, facts.Relation{
									Kind:   facts.RelImplements,
									Target: nodeText(t, src),
								})
							}
						}
					}
				}
			}
		}

		classBody := findChildByKind(node, "class_body")
		classifySymbol(&f, symbolName, classBody, ctx, facts.SymbolClass)
		result = append(result, f)

		// Extract class methods
		if classBody != nil {
			for j := range classBody.ChildCount() {
				member := classBody.Child(j)
				if member.Kind() != "method_definition" && member.Kind() != "public_field_definition" {
					continue
				}
				methodName := findChildByKind(member, "property_identifier")
				if methodName == nil {
					methodName = findChildByKind(member, "identifier")
				}
				if methodName == nil {
					continue
				}
				mName := nodeText(methodName, src)
				if strings.HasPrefix(mName, "#") || mName == "constructor" {
					continue
				}
				isPrivate := false
				for k := range member.ChildCount() {
					c := member.Child(k)
					if c.Kind() == "accessibility_modifier" && nodeText(c, src) == "private" {
						isPrivate = true
						break
					}
				}
				mRels := []facts.Relation{{Kind: facts.RelDeclares, Target: dir}}
				callRels, m := collectCallsWithMetrics(member, src, dir, symbolName, ctx.importMap, dir+"."+symbolName+"."+mName, mName)
				mRels = append(mRels, callRels...)
				mProps := map[string]any{
					"symbol_kind": facts.SymbolMethod,
					"exported":    isExported && !isPrivate,
					"language":    "typescript",
					"receiver":    symbolName,
				}
				applyTSMetrics(mProps, m)
				result = append(result, facts.Fact{
					Kind:      facts.KindSymbol,
					Name:      dir + "." + symbolName + "." + mName,
					File:      relFile,
					Line:      int(member.StartPosition().Row) + 1,
					Props:     mProps,
					Relations: mRels,
				})
			}
		}

	case "interface_declaration":
		if name := findChildByKind(node, "type_identifier"); name != nil {
			result = append(result, e.simpleSymbol(node, ctx, nodeText(name, src), facts.SymbolInterface, isExported))
		}

	case "type_alias_declaration":
		if name := findChildByKind(node, "type_identifier"); name != nil {
			result = append(result, e.simpleSymbol(node, ctx, nodeText(name, src), facts.SymbolType, isExported))
		}

	case "enum_declaration":
		name := findChildByKind(node, "identifier")
		if name == nil {
			name = findChildByKind(node, "type_identifier")
		}
		if name != nil {
			result = append(result, e.simpleSymbol(node, ctx, nodeText(name, src), facts.SymbolEnum, isExported))
		}

	case "internal_module", "module":
		// TypeScript `namespace X {}` / `module X {}`.
		name := findChildByKind(node, "identifier")
		if name == nil {
			name = findChildByKind(node, "nested_identifier")
		}
		if name != nil {
			result = append(result, e.simpleSymbol(node, ctx, nodeText(name, src), "namespace", isExported))
		}

	case "lexical_declaration", "variable_declaration":
		for j := range node.ChildCount() {
			decl := node.Child(j)
			if decl.Kind() != "variable_declarator" {
				continue
			}
			name := findChildByKind(decl, "identifier")
			if name == nil {
				continue
			}
			symbolName := nodeText(name, src)

			// Determine the value node and the symbol kind. Arrow functions and
			// memo/forwardRef-wrapped values are functions/components; everything
			// else is a plain variable.
			symbolKind := facts.SymbolVariable
			var body *sitter.Node
			if v := findChildByKind(decl, "arrow_function"); v != nil {
				symbolKind = facts.SymbolFunc
				body = v
			} else if call := findChildByKind(decl, "call_expression"); call != nil && isComponentWrapper(call, src) {
				symbolKind = facts.SymbolFunc
				body = call
			}

			vRels := []facts.Relation{{Kind: facts.RelDeclares, Target: dir}}
			var vMetrics *tsBodyMetrics
			if body != nil {
				callRels, m := collectCallsWithMetrics(body, src, dir, "", ctx.importMap, dir+"."+symbolName, symbolName)
				vRels = append(vRels, callRels...)
				vMetrics = m
			}
			f := facts.Fact{
				Kind: facts.KindSymbol,
				Name: dir + "." + symbolName,
				File: relFile,
				Line: int(node.StartPosition().Row) + 1,
				Props: map[string]any{
					"symbol_kind": symbolKind,
					"exported":    isExported,
					"language":    "typescript",
				},
				Relations: vRels,
			}
			if symbolKind == facts.SymbolFunc {
				applyTSMetrics(f.Props, vMetrics)
			}
			classifySymbol(&f, symbolName, body, ctx, symbolKind)
			result = append(result, f)
		}
	}

	return result
}

// funcSymbol builds a function/component symbol fact. declNode supplies the source
// location; body is walked for outgoing calls and JSX-based classification.
func (e *TSExtractor) funcSymbol(declNode, body *sitter.Node, ctx *extractCtx, name string, exported bool) facts.Fact {
	rels := []facts.Relation{{Kind: facts.RelDeclares, Target: ctx.dir}}
	callRels, m := collectCallsWithMetrics(body, ctx.src, ctx.dir, "", ctx.importMap, ctx.dir+"."+name, name)
	rels = append(rels, callRels...)
	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: ctx.dir + "." + name,
		File: ctx.relFile,
		Line: int(declNode.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolFunc,
			"exported":    exported,
			"language":    "typescript",
		},
		Relations: rels,
	}
	applyTSMetrics(f.Props, m)
	classifySymbol(&f, name, body, ctx, facts.SymbolFunc)
	return f
}

// simpleSymbol builds a declaration-only symbol fact (interface, type, enum, namespace).
func (e *TSExtractor) simpleSymbol(node *sitter.Node, ctx *extractCtx, name, kind string, exported bool) facts.Fact {
	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: ctx.dir + "." + name,
		File: ctx.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": kind,
			"exported":    exported,
			"language":    "typescript",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: ctx.dir}},
	}
	return f
}

// detectRoute checks if a file path corresponds to a Next.js route.
func detectRoute(relFile string) *facts.Fact {
	// Next.js App Router: app/**/page.tsx, app/**/route.tsx
	// Next.js Pages Router: pages/**/*.tsx

	parts := strings.Split(filepath.ToSlash(relFile), "/")

	// App Router
	for i, p := range parts {
		if p == "app" && i < len(parts)-1 {
			fileName := parts[len(parts)-1]
			baseName := strings.TrimSuffix(strings.TrimSuffix(fileName, ".tsx"), ".ts")

			if baseName == "page" || baseName == "route" || baseName == "layout" || baseName == "loading" || baseName == "error" {
				// Strip Next.js route groups — directory segments wrapped in ()
				// that act as layout organizers without affecting the URL.
				// e.g. (standard), (client-data), (header) → removed from path.
				segParts := parts[i+1 : len(parts)-1]
				urlParts := make([]string, 0, len(segParts))
				for _, seg := range segParts {
					if len(seg) >= 2 && seg[0] == '(' && seg[len(seg)-1] == ')' {
						continue // route group — not part of the URL
					}
					urlParts = append(urlParts, seg)
				}

				routePath := "/" + strings.Join(urlParts, "/")
				if routePath == "/" {
					routePath = "/"
				}

				method := "GET"
				if baseName == "route" {
					method = "ALL" // API route handler
				}

				return &facts.Fact{
					Kind: facts.KindRoute,
					Name: routePath,
					File: relFile,
					Line: 1,
					Props: map[string]any{
						"method":    method,
						"type":      baseName,
						"router":    "app",
						"language":  "typescript",
						"framework": "nextjs",
					},
				}
			}
		}
	}

	// Pages Router
	for i, p := range parts {
		if p == "pages" && i < len(parts)-1 {
			remaining := parts[i+1:]
			fileName := remaining[len(remaining)-1]
			baseName := strings.TrimSuffix(strings.TrimSuffix(fileName, ".tsx"), ".ts")

			// Skip _app, _document, _error
			if strings.HasPrefix(baseName, "_") {
				return nil
			}

			routeParts := make([]string, 0, len(remaining))
			for j, rp := range remaining {
				if j == len(remaining)-1 {
					if baseName != "index" {
						routeParts = append(routeParts, baseName)
					}
				} else {
					routeParts = append(routeParts, rp)
				}
			}

			routePath := "/" + strings.Join(routeParts, "/")

			// Detect API routes
			isAPI := len(remaining) > 0 && remaining[0] == "api"
			method := "GET"
			if isAPI {
				method = "ALL"
			}

			return &facts.Fact{
				Kind: facts.KindRoute,
				Name: routePath,
				File: relFile,
				Line: 1,
				Props: map[string]any{
					"method":    method,
					"type":      "page",
					"router":    "pages",
					"language":  "typescript",
					"framework": "nextjs",
				},
			}
		}
	}

	return nil
}

// detectNextJS checks if the repository is a Next.js project.
// It searches the TypeScript root directory (which may be a subdirectory in a
// monorepo) for next.config.* files or a package.json with a "next" dependency.
func detectNextJS(repoPath string) bool {
	tsRoot, _ := findTSRoot(repoPath)
	return detectNextJSAt(tsRoot) || (tsRoot != repoPath && detectNextJSAt(repoPath))
}

func detectNextJSAt(dir string) bool {
	// Check next.config.* at this directory level
	for _, name := range []string{"next.config.js", "next.config.mjs", "next.config.ts"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}

	// Check package.json for next dependency
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg map[string]any
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	for _, key := range []string{"dependencies", "devDependencies"} {
		if deps, ok := pkg[key].(map[string]any); ok {
			if _, ok := deps["next"]; ok {
				return true
			}
		}
	}
	return false
}

func isTypeScriptFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".ts" || ext == ".tsx" || ext == ".vue" || ext == ".js" || ext == ".jsx" || ext == ".svelte"
}

// OwnsFile implements plugin.FileOwner for incremental caching.
func (e *TSExtractor) OwnsFile(relFile string) bool { return isTypeScriptFile(relFile) }

// hasChildKind reports whether node has a direct child of the given kind.
func hasChildKind(node *sitter.Node, kind string) bool {
	return findChildByKind(node, kind) != nil
}

// firstDeclChild returns the first named declaration child of an export_statement,
// or nil if the export wraps something else (a value, re-export clause, etc.).
func firstDeclChild(node *sitter.Node) *sitter.Node {
	for _, k := range []string{
		"function_declaration", "generator_function_declaration",
		"class_declaration", "abstract_class_declaration",
		"interface_declaration", "type_alias_declaration",
		"lexical_declaration", "variable_declaration",
		"enum_declaration", "internal_module", "module",
	} {
		if c := findChildByKind(node, k); c != nil {
			return c
		}
	}
	return nil
}

// fileSymbolName derives a symbol name from a file path for anonymous default
// exports. Generic Next.js filenames (page, route, layout, …) are disambiguated
// with their parent directory segment, e.g. app/dashboard/page.tsx → "DashboardPage".
func fileSymbolName(relFile string) string {
	base := filepath.Base(relFile)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	switch base {
	case "index", "page", "route", "layout", "loading", "error", "not-found", "template", "default",
		"+page", "+layout", "+error", "+server":
		parent := filepath.Base(filepath.Dir(relFile))
		if parent != "" && parent != "." && parent != string(filepath.Separator) {
			return toPascal(parent) + toPascal(base)
		}
	}
	return toPascal(base)
}

// toPascal converts an arbitrary identifier-ish string into PascalCase, splitting
// on any non-alphanumeric characters (e.g. "my-component" → "MyComponent").
func toPascal(s string) string {
	var b strings.Builder
	upNext := true
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			if upNext && r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
			upNext = false
		default:
			upNext = true
		}
	}
	return b.String()
}

// collectExportedLocalNames returns the set of locally-declared names that are
// exported via a separate `export { A, B as C }` clause or `export default Name`
// statement (where the declaration itself carries no inline export keyword).
func collectExportedLocalNames(root *sitter.Node, src []byte) map[string]bool {
	out := make(map[string]bool)
	for i := range root.ChildCount() {
		child := root.Child(i)
		if child.Kind() != "export_statement" {
			continue
		}
		// export { A, B as C }
		if clause := findChildByKind(child, "export_clause"); clause != nil {
			for j := range clause.ChildCount() {
				spec := clause.Child(j)
				if spec.Kind() != "export_specifier" {
					continue
				}
				if n := spec.ChildByFieldName("name"); n != nil {
					out[nodeText(n, src)] = true
				}
			}
			continue
		}
		// export default Name
		if hasChildKind(child, "default") {
			if id := findChildByKind(child, "identifier"); id != nil {
				out[nodeText(id, src)] = true
			}
		}
	}
	return out
}

// reactHTTPMethods are the App Router route-handler export names.
var reactHTTPMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "HEAD": true, "OPTIONS": true,
}

// classifySymbol enriches a symbol fact with React/Next.js semantic props
// (web_component, framework, and for route handlers method), mirroring the
// ios_component/framework classification used by the Swift extractor. body, when
// non-nil, is scanned for JSX to confirm component-ness in non-TSX files.
func classifySymbol(f *facts.Fact, name string, body *sitter.Node, ctx *extractCtx, symbolKind string) {
	// Next.js App Router route handler: GET/POST/... in a route.{ts,tsx} file.
	if symbolKind == facts.SymbolFunc && reactHTTPMethods[name] && isAppRouteFile(ctx.relFile) {
		f.Props["web_component"] = "route_handler"
		f.Props["method"] = name
		f.Props["framework"] = "nextjs"
		return
	}
	// Composable (Vue/Nuxt) or hook (React): a useXxx function.
	if symbolKind == facts.SymbolFunc && isHookName(name) {
		if ctx.isVue || ctx.isNuxt {
			f.Props["web_component"] = "composable"
			if ctx.isNuxt {
				f.Props["framework"] = "nuxt"
			} else {
				f.Props["framework"] = "vue"
			}
		} else {
			f.Props["web_component"] = "hook"
			f.Props["framework"] = "react"
		}
		return
	}
	// React component: a PascalCase function/class that renders JSX. In .tsx/.jsx
	// files a PascalCase function/class is treated as a component; elsewhere we
	// require literal JSX in the body to avoid misclassifying plain classes.
	if isComponentName(name) && (symbolKind == facts.SymbolFunc || symbolKind == facts.SymbolClass) {
		if ctx.isTSX || (body != nil && containsJSX(body)) {
			f.Props["web_component"] = "component"
			if ctx.isNextJS {
				f.Props["framework"] = "nextjs"
			} else {
				f.Props["framework"] = "react"
			}
		}
	}
}

// isHookName reports whether name follows the React hook convention useXxx.
func isHookName(name string) bool {
	if !strings.HasPrefix(name, "use") || len(name) < 4 {
		return false
	}
	c := name[3]
	return c >= 'A' && c <= 'Z'
}

// isComponentName reports whether name is PascalCase (a React component convention).
func isComponentName(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// isAppRouteFile reports whether relFile is a Next.js App Router route handler
// file (a route.{ts,tsx} under an "app" directory segment).
func isAppRouteFile(relFile string) bool {
	base := filepath.Base(relFile)
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".tsx"), ".ts")
	if base != "route" {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(relFile), "/") {
		if seg == "app" {
			return true
		}
	}
	return false
}

// containsJSX reports whether the subtree rooted at node contains a JSX element.
func containsJSX(node *sitter.Node) bool {
	if node == nil {
		return false
	}
	switch node.Kind() {
	case "jsx_element", "jsx_self_closing_element", "jsx_fragment":
		return true
	}
	for i := range node.ChildCount() {
		if containsJSX(node.Child(i)) {
			return true
		}
	}
	return false
}

// isComponentWrapper reports whether a call expression wraps a component, i.e. it
// calls memo / forwardRef (optionally as React.memo / React.forwardRef).
func isComponentWrapper(call *sitter.Node, src []byte) bool {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return false
	}
	name := ""
	switch fn.Kind() {
	case "identifier":
		name = nodeText(fn, src)
	case "member_expression":
		if prop := fn.ChildByFieldName("property"); prop != nil {
			name = nodeText(prop, src)
		}
	}
	return name == "memo" || name == "forwardRef"
}

func findChildByKind(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := range node.ChildCount() {
		child := node.Child(i)
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

func nodeText(node *sitter.Node, src []byte) string {
	return string(src[node.StartByte():node.EndByte()])
}

// tsAliasRoot is a directory (repoPath-relative, "" = root) and the alias
// map its tsconfig declares, already qualified with dir as a prefix.
type tsAliasRoot struct {
	dir     string
	aliases map[string]string
}

// collectTSAliasRoots finds every directory whose tsconfig.json (or
// tsconfig.base.json) declares path aliases — unlike findTSRoot, which stops
// at the first match, this covers monorepos with one tsconfig per package.
func collectTSAliasRoots(repoPath string) []tsAliasRoot {
	maxDepth := 2
	if isDeepNestedProject(repoPath) {
		maxDepth = 8
	}
	var roots []tsAliasRoot
	walkTSAliasRoots(repoPath, repoPath, 0, maxDepth, &roots)
	return roots
}

func walkTSAliasRoots(repoPath, dir string, depth, maxDepth int, out *[]tsAliasRoot) {
	if aliases, ok := aliasesAtDir(dir); ok {
		rel, err := filepath.Rel(repoPath, dir)
		if err != nil || rel == "." {
			rel = ""
		}
		rel = filepath.ToSlash(rel)

		// Concatenation, not filepath.Join, to preserve the trailing slash
		// resolveImportPath's `replacement + rest` depends on.
		qualified := make(map[string]string, len(aliases))
		for prefix, replacement := range aliases {
			if rel != "" {
				replacement = rel + "/" + replacement
			}
			qualified[prefix] = replacement
		}
		*out = append(*out, tsAliasRoot{dir: rel, aliases: qualified})
	}
	if depth >= maxDepth {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || tsSkipDirs[entry.Name()] {
			continue
		}
		walkTSAliasRoots(repoPath, filepath.Join(dir, entry.Name()), depth+1, maxDepth, out)
	}
}

// aliasesAtDir tries tsconfig.json then tsconfig.base.json at dir, returning
// the first one that declares a non-empty paths map.
func aliasesAtDir(dir string) (map[string]string, bool) {
	for _, name := range []string{"tsconfig.json", "tsconfig.base.json"} {
		if aliases, ok := tryParseTSConfigAliases(filepath.Join(dir, name)); ok {
			return aliases, true
		}
	}
	return nil, false
}

// aliasesForDir returns the alias map of the root whose dir is the longest
// matching ancestor-or-equal prefix of dir, or nil if none match.
func aliasesForDir(roots []tsAliasRoot, dir string) map[string]string {
	dir = filepath.ToSlash(dir)
	var best *tsAliasRoot
	bestLen := -1
	for i := range roots {
		r := &roots[i]
		if r.dir != "" && dir != r.dir && !strings.HasPrefix(dir, r.dir+"/") {
			continue
		}
		if len(r.dir) > bestLen {
			best = r
			bestLen = len(r.dir)
		}
	}
	if best == nil {
		return nil
	}
	return best.aliases
}

// withSvelteKitLibDefault adds the "$lib/" -> "<root>/src/lib/" convention
// to every root that doesn't already define it.
func withSvelteKitLibDefault(roots []tsAliasRoot) []tsAliasRoot {
	if len(roots) == 0 {
		roots = []tsAliasRoot{{dir: "", aliases: map[string]string{}}}
	}
	for i := range roots {
		if _, ok := roots[i].aliases["$lib/"]; ok {
			continue
		}
		target := "src/lib/"
		if roots[i].dir != "" {
			target = roots[i].dir + "/src/lib/"
		}
		roots[i].aliases["$lib/"] = target
	}
	return roots
}

// tryParseTSConfigAliases reads path alias mappings from a tsconfig.json,
// e.g. "@/*": ["./src/*"] maps prefix "@/" to replacement "src/". ok is
// false if the file is missing/invalid or declares no usable paths.
func tryParseTSConfigAliases(tsconfigPath string) (map[string]string, bool) {
	data, err := os.ReadFile(tsconfigPath)
	if err != nil {
		return nil, false
	}

	var config struct {
		CompilerOptions struct {
			Paths map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, false
	}

	aliases := make(map[string]string)
	for pattern, targets := range config.CompilerOptions.Paths {
		if len(targets) == 0 {
			continue
		}
		// "@/*": ["./src/*"] → prefix "@/" maps to replacement "src/"
		if strings.HasSuffix(pattern, "*") && strings.HasSuffix(targets[0], "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			replacement := strings.TrimSuffix(targets[0], "*")
			replacement = strings.TrimPrefix(replacement, "./")
			aliases[prefix] = replacement
		}
	}
	return aliases, len(aliases) > 0
}

// resolveImportPath normalizes a TypeScript import path to a filesystem-relative path.
// It handles path aliases (@/), relative imports (./), and identifies external packages.
func resolveImportPath(importPath, fileDir string, aliases map[string]string) (string, bool) {
	// Try alias resolution first
	for prefix, replacement := range aliases {
		if strings.HasPrefix(importPath, prefix) {
			rest := strings.TrimPrefix(importPath, prefix)
			return filepath.ToSlash(filepath.Clean(replacement + rest)), false
		}
	}

	// Relative imports
	if strings.HasPrefix(importPath, ".") {
		resolved := filepath.ToSlash(filepath.Clean(filepath.Join(fileDir, importPath)))
		return resolved, false
	}

	// Everything else is external (react, next/image, @tanstack/react-query, etc.)
	return importPath, true
}

// tsModuleExts are the source extensions a bare import path may resolve to, tried in
// TS-before-JS order (a project with both prefers the typed file).
var tsModuleExts = []string{".ts", ".tsx", ".js", ".jsx", ".vue", ".svelte"}

// resolveModuleFile resolves an extensionless internal import path to the actual
// source file backing it, using the set of known indexed files. It returns the
// resolved file path, the directory that owns its symbols, and whether a match was
// found. A file module (`./utils` → utils.ts) owns symbols under its PARENT dir (the
// symbol naming convention "<dir>.<sym>" uses filepath.Dir of the file); a folder
// module (`./feed_item` → feed_item/index.tsx) owns symbols under the folder itself.
// This is what lets a default import bind to the folder-index default export, whose
// name (fileSymbolName → "<Folder>Index") is otherwise unmatchable.
func resolveModuleFile(resolved string, knownFiles map[string]bool) (indexPath, dir string, ok bool) {
	resolved = filepath.ToSlash(resolved)
	for _, ext := range tsModuleExts {
		if knownFiles[resolved+ext] {
			return resolved + ext, filepath.ToSlash(filepath.Dir(resolved)), true
		}
	}
	for _, ext := range tsModuleExts {
		if idx := resolved + "/index" + ext; knownFiles[idx] {
			return idx, resolved, true
		}
	}
	return "", "", false
}

// buildImportSymbols returns a map of locally-bound import name → canonical symbol
// fact name for named imports from internal modules. It lets bare calls to
// imported functions (e.g. `formatName()`) resolve to the callee's declaration
// fact. Symbols declared in an imported module are named "<moduleDir>.<exportName>",
// where moduleDir is the directory of the resolved module file — this matches the
// common file-module case (e.g. import "./utils" → utils.ts → "<dir>.foo").
func buildImportSymbols(root *sitter.Node, src []byte, relFile string, aliases map[string]string) map[string]string {
	fileDir := filepath.Dir(relFile)
	m := make(map[string]string)
	for i := range root.ChildCount() {
		child := root.Child(i)
		if child.Kind() != "import_statement" {
			continue
		}
		source := findChildByKind(child, "string")
		if source == nil {
			continue
		}
		importPath := strings.Trim(nodeText(source, src), `"'`)
		resolved, isExternal := resolveImportPath(importPath, fileDir, aliases)
		if isExternal {
			continue // external modules have no local declaration facts
		}
		moduleDir := filepath.Dir(resolved)

		clause := findChildByKind(child, "import_clause")
		if clause == nil {
			continue
		}
		named := findChildByKind(clause, "named_imports")
		if named == nil {
			continue // default/namespace imports are not resolved
		}
		for j := range named.ChildCount() {
			spec := named.Child(j)
			if spec.Kind() != "import_specifier" {
				continue
			}
			nameNode := spec.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			exportName := nodeText(nameNode, src)
			local := exportName
			if aliasNode := spec.ChildByFieldName("alias"); aliasNode != nil {
				local = nodeText(aliasNode, src)
			}
			m[local] = moduleDir + "." + exportName
		}
	}
	return m
}

// collectTSFileRefs performs a whole-file reference pass for the dead-code detector.
//
// The per-function call walk (collectCallsWithMetrics) only records call_expression
// edges inside function bodies, so it misses the ways a React/CommonJS codebase
// actually uses a symbol: rendering a component in JSX (<Foo/>), passing an imported
// identifier as a value (route configs — `{ component: Foo }`), namespace member
// access (`ns.foo`), and require()-bound names. Many of these live at module scope
// (top-level route arrays), which has no enclosing symbol fact to hang an edge on.
//
// We fold every such reference into a single KindFileRef fact — the reference-only
// carrier the dead-code detector already consumes for top-level references — so
// genuinely-used code is not mis-reported as dead. References are matched downstream
// by short name, so binding a local import name to "<moduleDir>.<name>" is enough
// even when the canonical declaration lives behind a folder index (the last segment
// still matches). This only ever ADDS references, so it can hide a real orphan but
// never invent a false one — the detector's deliberate conservative bias.
func (e *TSExtractor) collectTSFileRefs(root *sitter.Node, ctx *extractCtx, aliases map[string]string) []facts.Fact {
	src := ctx.src
	fileDir := filepath.Dir(ctx.relFile)
	internal := make(map[string]string)   // local name -> canonical target (internal modules only)
	namespaces := make(map[string]string) // `import * as ns` local -> module dir
	var reexports []string                // canonical targets re-exported via `export { x } from './y'`
	var defaultRefs []string              // default-export targets of default-imported modules

	bind := func(local, moduleDir, exportName string) {
		if local != "" {
			internal[local] = moduleDir + "." + exportName
		}
	}
	// resolveModule resolves an import specifier to its owning module directory and,
	// when the backing file is known, that file's path (for computing the module's
	// default-export name). ok is false for external modules. It prefers the exact
	// file/folder-index from the known-files set — so a folder module resolves to the
	// folder itself, not its parent — falling back to filepath.Dir when the target
	// isn't indexed (still lets short-name matching link named imports).
	resolveModule := func(node *sitter.Node) (moduleDir, indexPath string, ok bool) {
		if node == nil {
			return "", "", false
		}
		importPath := strings.Trim(nodeText(node, src), `"'`)
		resolved, isExternal := resolveImportPath(importPath, fileDir, aliases)
		if isExternal {
			return "", "", false
		}
		if idx, dir, found := resolveModuleFile(resolved, ctx.knownFiles); found {
			return dir, idx, true
		}
		return filepath.Dir(resolved), "", true
	}

	// Pass 1: parse the bindings — static imports, `export … from` re-exports, and
	// require()/dynamic-import assignments — into name → target maps.
	for i := range root.ChildCount() {
		child := root.Child(i)
		switch child.Kind() {
		case "import_statement":
			moduleDir, indexPath, ok := resolveModule(findChildByKind(child, "string"))
			if !ok {
				continue
			}
			clause := findChildByKind(child, "import_clause")
			if clause == nil {
				continue
			}
			for j := range clause.ChildCount() {
				c := clause.Child(j)
				switch c.Kind() {
				case "identifier": // default import: `import Foo from './x'`
					local := nodeText(c, src)
					bind(local, moduleDir, local)
					// A default import IS a use of the module's default export, whose
					// symbol is named by fileSymbolName (an anonymous
					// `export default connect(...)(X)` in a folder index becomes
					// "<Folder>Index" — unmatchable by the local name). Record it so the
					// wrapper symbol is not falsely reported dead.
					if indexPath != "" {
						defaultRefs = append(defaultRefs, moduleDir+"."+fileSymbolName(indexPath))
					}
				case "namespace_import": // `import * as ns from './x'`
					if id := findChildByKind(c, "identifier"); id != nil {
						namespaces[nodeText(id, src)] = moduleDir
					}
				case "named_imports":
					for k := range c.ChildCount() {
						spec := c.Child(k)
						if spec.Kind() != "import_specifier" {
							continue
						}
						nameNode := spec.ChildByFieldName("name")
						if nameNode == nil {
							continue
						}
						exportName := nodeText(nameNode, src)
						local := exportName
						if a := spec.ChildByFieldName("alias"); a != nil {
							local = nodeText(a, src)
						}
						bind(local, moduleDir, exportName)
					}
				}
			}
		case "export_statement":
			// `export { a, default as b } from './y'` re-exports y's symbols; record a
			// reference to each so a symbol consumed only through a barrel is not
			// mis-reported as dead (matched by short name downstream).
			moduleDir, indexPath, ok := resolveModule(child.ChildByFieldName("source"))
			if !ok {
				continue
			}
			if clause := findChildByKind(child, "export_clause"); clause != nil {
				for k := range clause.ChildCount() {
					spec := clause.Child(k)
					if spec.Kind() != "export_specifier" {
						continue
					}
					nameNode := spec.ChildByFieldName("name")
					if nameNode == nil {
						continue
					}
					// `export { default as X } from './y'` re-exports y's default; the
					// literal name "default" matches no symbol, so resolve it to y's
					// default-export name (fileSymbolName) instead.
					if name := nodeText(nameNode, src); name == "default" && indexPath != "" {
						reexports = append(reexports, moduleDir+"."+fileSymbolName(indexPath))
					} else {
						reexports = append(reexports, moduleDir+"."+name)
					}
				}
			}
		case "lexical_declaration", "variable_declaration":
			// CommonJS: `const x = require('./y')` / `const { a } = require('./y')`.
			for j := range child.ChildCount() {
				d := child.Child(j)
				if d.Kind() != "variable_declarator" {
					continue
				}
				val := d.ChildByFieldName("value")
				if val == nil || val.Kind() != "call_expression" {
					continue
				}
				fn := val.ChildByFieldName("function")
				if fn == nil || nodeText(fn, src) != "require" {
					continue
				}
				moduleDir, _, ok := resolveModule(findChildByKind(val.ChildByFieldName("arguments"), "string"))
				if !ok {
					continue
				}
				nameNode := d.ChildByFieldName("name")
				if nameNode == nil {
					continue
				}
				switch nameNode.Kind() {
				case "identifier":
					local := nodeText(nameNode, src)
					bind(local, moduleDir, local)
				case "object_pattern":
					for k := range nameNode.ChildCount() {
						if p := nameNode.Child(k); p.Kind() == "shorthand_property_identifier_pattern" {
							nm := nodeText(p, src)
							bind(nm, moduleDir, nm)
						}
					}
				}
			}
		}
	}

	// Pass 2: walk the whole tree collecting references — to the imported/required
	// bindings above and, in call positions, to same-module declarations.
	var targets []string
	seen := make(map[string]bool)
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Kind() {
		case "import_statement":
			return // binding sites, not uses
		case "identifier", "type_identifier":
			// type_identifier covers an imported type/interface used only as an
			// annotation (`repo: Repo`), which is otherwise never an edge.
			if t, ok := internal[nodeText(n, src)]; ok {
				add(t)
			}
			return
		case "member_expression":
			obj := n.ChildByFieldName("object")
			prop := n.ChildByFieldName("property")
			if obj != nil && obj.Kind() == "identifier" {
				name := nodeText(obj, src)
				if dir, ok := namespaces[name]; ok && prop != nil {
					add(dir + "." + nodeText(prop, src)) // ns.foo -> <dir>.foo
					return
				}
				if t, ok := internal[name]; ok {
					add(t) // Foo.bar on imported Foo marks Foo used
				}
			}
			for i := range n.ChildCount() {
				walk(n.Child(i))
			}
			return
		case "jsx_opening_element", "jsx_self_closing_element":
			if nameNode := n.ChildByFieldName("name"); nameNode != nil {
				add(resolveJSXTag(nameNode, src, ctx.dir, internal, namespaces))
			}
			for i := range n.ChildCount() {
				walk(n.Child(i))
			}
			return
		case "call_expression":
			// A bare callee and identifier arguments are USE positions (never
			// declarations), so it is safe to resolve them same-module as well as via
			// imports. This catches module-scope calls the per-function walk cannot see
			// (`startSession()` at file top level) and functions passed as values
			// (`connect(mapStateToProps, actions)`, HOC/callback wiring) — otherwise a
			// symbol used only that way is falsely reported dead.
			if fn := n.ChildByFieldName("function"); fn != nil && fn.Kind() == "identifier" {
				add(resolveLocalOrImport(nodeText(fn, src), ctx.dir, internal))
			}
			if args := n.ChildByFieldName("arguments"); args != nil {
				for i := range args.ChildCount() {
					if a := args.Child(i); a.Kind() == "identifier" {
						add(resolveLocalOrImport(nodeText(a, src), ctx.dir, internal))
					}
				}
			}
			for i := range n.ChildCount() {
				walk(n.Child(i))
			}
			return
		}
		for i := range n.ChildCount() {
			walk(n.Child(i))
		}
	}
	walk(root)

	for _, t := range reexports {
		add(t)
	}
	for _, t := range defaultRefs {
		add(t)
	}
	if len(targets) == 0 {
		return nil
	}

	rels := make([]facts.Relation, 0, len(targets))
	for _, t := range targets {
		rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: t})
	}
	return []facts.Fact{{
		Kind:      facts.KindFileRef,
		Name:      ctx.relFile,
		File:      ctx.relFile,
		Line:      1,
		Props:     map[string]any{"language": "typescript"},
		Relations: rels,
	}}
}

// resolveLocalOrImport resolves a bare name used in a value/call position to its
// canonical symbol target: the imported binding when it is one, otherwise the
// same-module declaration "<dir>.<name>". A name that matches neither yields a target
// no symbol fact will match (harmless), so this only ever adds real references.
func resolveLocalOrImport(name, dir string, internal map[string]string) string {
	if t, ok := internal[name]; ok {
		return t
	}
	return dir + "." + name
}

// resolveJSXTag resolves a JSX element's tag name to a canonical symbol target, or ""
// when it is a host element (`<div>`) or an unresolvable/external component. A bare
// PascalCase tag that is not an import is resolved same-module (`<dir>.<Name>`) so a
// component rendered only by a sibling in the same file is not flagged dead.
func resolveJSXTag(nameNode *sitter.Node, src []byte, dir string, internal, namespaces map[string]string) string {
	switch nameNode.Kind() {
	case "identifier":
		name := nodeText(nameNode, src)
		if t, ok := internal[name]; ok {
			return t
		}
		if isComponentName(name) {
			return dir + "." + name
		}
	case "member_expression", "nested_identifier":
		// <Foo.Bar/> — resolve via the root object Foo.
		obj := nameNode.ChildByFieldName("object")
		if obj == nil && nameNode.ChildCount() > 0 {
			obj = nameNode.Child(0)
		}
		if obj != nil {
			root := nodeText(obj, src)
			if d, ok := namespaces[root]; ok {
				if prop := nameNode.ChildByFieldName("property"); prop != nil {
					return d + "." + nodeText(prop, src)
				}
			}
			if t, ok := internal[root]; ok {
				return t
			}
		}
	}
	return ""
}

// tsBodyMetrics accumulates per-function complexity signals during the single
// body walk — mirrors the Go/Python/Ruby/Swift/Kotlin extractors.
type tsBodyMetrics struct {
	loopDepth   int             // max loop nesting depth
	loopCount   int             // number of loop constructs (syntactic + array-method callbacks)
	decisions   int             // decision points (cyclomatic = 1 + decisions)
	callsInLoop []string        // distinct call targets invoked at loop depth >= 1
	inLoopSeen  map[string]bool // dedup set for callsInLoop
	recursive   bool            // body directly calls the enclosing function
}

// tsIterators are array/collection methods whose callback runs once per element —
// i.e. a loop. A function/arrow argument to a method NOT in this set (setTimeout,
// addEventListener, .then/.catch, JSX event handlers, useEffect) runs once or
// later and is not treated as a loop. Aggregate-or-iterate names (some/every/find)
// are safe to include because a callback must be present before any counts.
var tsIterators = map[string]bool{
	"map": true, "forEach": true, "filter": true, "flatMap": true,
	"reduce": true, "reduceRight": true, "some": true, "every": true,
	"find": true, "findIndex": true, "findLast": true, "findLastIndex": true,
	"sort": true, "flat": true, "group": true, "partition": true,
}

// tsCheapMethods are obviously-cheap methods that are not I/O. No-arg-ish method
// calls to these inside loops are not recorded in calls_in_loop, keeping it focused
// (the enterprise keyword gate is the real precision filter).
var tsCheapMethods = map[string]bool{
	"toString": true, "push": true, "pop": true, "shift": true, "unshift": true,
	"slice": true, "splice": true, "join": true, "concat": true, "includes": true,
	"indexOf": true, "length": true, "trim": true, "split": true, "replace": true,
	"keys": true, "values": true, "entries": true, "then": true, "catch": true,
	"finally": true, "bind": true, "call": true, "apply": true, "has": true,
	"get": true, "set": true, "add": true, "delete": true, "toFixed": true,
	"map": true, "forEach": true, "filter": true, "reduce": true, "sort": true,
	"toLowerCase": true, "toUpperCase": true, "toLocaleLowerCase": true,
	"toLocaleUpperCase": true, "startsWith": true, "endsWith": true,
	"charAt": true, "padStart": true, "padEnd": true, "repeat": true,
	// Date / number formatters — cheap per-row work, not I/O.
	"toLocaleDateString": true, "toLocaleString": true, "toLocaleTimeString": true,
	"toISOString": true, "getTime": true, "getFullYear": true, "getMonth": true,
	"getDate": true, "getHours": true, "getMinutes": true, "getDay": true,
}

// tsIsFunctionLike reports whether a node introduces a function scope (a deferred
// body). Calls inside such a body are decoupled from the enclosing loops.
func tsIsFunctionLike(kind string) bool {
	switch kind {
	case "arrow_function", "function_expression", "function_declaration",
		"function", "generator_function", "generator_function_declaration",
		"method_definition":
		return true
	}
	return false
}

func tsBooleanOp(node *sitter.Node) bool {
	for i := range node.ChildCount() {
		switch node.Child(i).Kind() {
		case "&&", "||", "??":
			return true
		}
	}
	return false
}

func tsByteContains(outer, inner *sitter.Node) bool {
	return inner.StartByte() >= outer.StartByte() && inner.EndByte() <= outer.EndByte()
}

// tsIteratorCallback returns the function/arrow callback of an array-iterator call
// (items.map(cb), items.forEach(cb)) — or nil if the call is not an iterator with a
// closure argument.
func tsIteratorCallback(call *sitter.Node, src []byte) *sitter.Node {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "member_expression" {
		return nil
	}
	prop := fn.ChildByFieldName("property")
	if prop == nil || !tsIterators[nodeText(prop, src)] {
		return nil
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	for i := range args.ChildCount() {
		switch c := args.Child(i); c.Kind() {
		case "arrow_function", "function_expression", "function":
			return c
		}
	}
	return nil
}

// tsMemberCall returns the receiver text and property name of a method call whose
// callee is a member_expression (`obj.method()` → "obj", "method"), for recording
// in-loop calls on unknown receivers. Returns "" property if not a member call.
func tsMemberCall(call *sitter.Node, src []byte) (recv, prop string) {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "member_expression" {
		return "", ""
	}
	p := fn.ChildByFieldName("property")
	if p == nil {
		return "", ""
	}
	prop = nodeText(p, src)
	if o := fn.ChildByFieldName("object"); o != nil {
		recv = nodeText(o, src)
	}
	return recv, prop
}

// tsBodyWalker walks a function/method body once, collecting call-edge relations
// and (when metrics != nil) per-function complexity signals.
type tsBodyWalker struct {
	src                 []byte
	dir, className      string
	importMap           map[string]string
	selfName, selfShort string
	metrics             *tsBodyMetrics
	loopDepth           int
	rels                []facts.Relation
	seen                map[string]bool
}

func (w *tsBodyWalker) recordCall(target string) {
	if w.metrics == nil || target == "" {
		return
	}
	if target == w.selfName || target == w.selfShort {
		w.metrics.recursive = true
	}
	w.recordInLoop(target)
}

func (w *tsBodyWalker) recordInLoop(target string) {
	if w.metrics == nil || target == "" || w.loopDepth == 0 {
		return
	}
	if w.metrics.inLoopSeen == nil {
		w.metrics.inLoopSeen = make(map[string]bool)
	}
	if !w.metrics.inLoopSeen[target] {
		w.metrics.inLoopSeen[target] = true
		w.metrics.callsInLoop = append(w.metrics.callsInLoop, target)
	}
}

func (w *tsBodyWalker) walk(n *sitter.Node) {
	if n == nil {
		return
	}
	kind := n.Kind()

	// A nested function/arrow definition is a deferred scope: its body runs when the
	// function is called, NOT per-iteration of the enclosing loops — so reset the
	// loop depth for its subtree. This is what stops a React event handler defined
	// inside a `.map(...)` render callback (`onClick={() => handleDelete(x)}`) from
	// being mis-counted as a per-iteration call. The iterator's own callback is
	// handled separately in the call_expression branch (its body walks at +1).
	if w.metrics != nil && tsIsFunctionLike(kind) {
		saved := w.loopDepth
		w.loopDepth = 0
		for i := range n.ChildCount() {
			w.walk(n.Child(i))
		}
		w.loopDepth = saved
		return
	}

	// Complexity metrics: count decision points so the single body walk doubles as
	// the cyclomatic pass.
	if w.metrics != nil {
		switch kind {
		case "if_statement", "ternary_expression", "switch_case", "catch_clause":
			w.metrics.decisions++
		case "binary_expression":
			if tsBooleanOp(n) {
				w.metrics.decisions++
			}
		}
	}

	// Syntactic loops: everything in the body runs per iteration.
	switch kind {
	case "for_statement", "for_in_statement", "while_statement", "do_statement":
		if w.metrics != nil {
			w.metrics.loopCount++
			w.metrics.decisions++
			if w.loopDepth+1 > w.metrics.loopDepth {
				w.metrics.loopDepth = w.loopDepth + 1
			}
		}
		w.loopDepth++
		for i := range n.ChildCount() {
			w.walk(n.Child(i))
		}
		w.loopDepth--
		return
	}

	if kind == "call_expression" {
		if target := resolveTSCall(n, w.src, w.dir, w.className, w.importMap); target != "" {
			if !w.seen[target] {
				w.seen[target] = true
				w.rels = append(w.rels, facts.Relation{Kind: facts.RelCalls, Target: target})
			}
			w.recordCall(target)
		} else if w.metrics != nil && w.loopDepth > 0 {
			// Method call on an unknown receiver inside a loop (repo.findMany(),
			// prisma.user.create()). No graph edge today, but its name feeds the perf
			// metric so the enterprise analyzer can flag per-iteration ORM/fetch I/O.
			if recv, prop := tsMemberCall(n, w.src); prop != "" && !tsCheapMethods[prop] {
				tgt := prop
				if recv != "" {
					tgt = recv + "." + prop
				}
				w.recordInLoop(tgt)
			}
		}
		// An array-iterator method with a callback (items.map(cb)) is a loop: its
		// callback body runs per element, but the receiver/other args run once.
		if w.metrics != nil {
			if cb := tsIteratorCallback(n, w.src); cb != nil {
				w.metrics.loopCount++
				w.metrics.decisions++
				if w.loopDepth+1 > w.metrics.loopDepth {
					w.metrics.loopDepth = w.loopDepth + 1
				}
				for i := range n.ChildCount() {
					if c := n.Child(i); tsByteContains(c, cb) {
						w.walkCallbackSubtree(c, cb)
					} else {
						w.walk(c)
					}
				}
				return
			}
		}
	}

	// A `this.<member>` reference inside a class method marks that member used, even
	// when it is not called: React binds event handlers as prop VALUES
	// (onClick={this.handleClick}), so a handler referenced only in JSX has no call
	// edge and would otherwise be mis-reported dead. className is the exact class
	// symbol name, so the target matches the method fact "<dir>.<Class>.<member>".
	if kind == "member_expression" && w.className != "" {
		if obj := n.ChildByFieldName("object"); obj != nil && obj.Kind() == "this" {
			if prop := n.ChildByFieldName("property"); prop != nil {
				target := w.dir + "." + w.className + "." + nodeText(prop, w.src)
				if !w.seen[target] {
					w.seen[target] = true
					w.rels = append(w.rels, facts.Relation{Kind: facts.RelCalls, Target: target})
				}
			}
		}
	}

	for i := range n.ChildCount() {
		w.walk(n.Child(i))
	}
}

// walkCallbackSubtree descends toward an iterator's callback, bumping the loop depth
// exactly at the callback (its body is per-iteration) while walking everything else
// (the receiver, sibling args) at the current depth.
func (w *tsBodyWalker) walkCallbackSubtree(n, cb *sitter.Node) {
	if n == nil {
		return
	}
	if n.StartByte() == cb.StartByte() && n.EndByte() == cb.EndByte() {
		// The iterator invokes this callback per element: walk its BODY at +1.
		// We descend into the callback's children directly rather than walk(cb),
		// because walk() would treat the callback as a function scope and reset
		// the depth — but THIS callback genuinely runs per iteration.
		w.loopDepth++
		for i := range cb.ChildCount() {
			w.walk(cb.Child(i))
		}
		w.loopDepth--
		return
	}
	for i := range n.ChildCount() {
		if c := n.Child(i); tsByteContains(c, cb) {
			w.walkCallbackSubtree(c, cb)
		} else {
			w.walk(c)
		}
	}
}

// collectCallsWithMetrics walks a function/method body subtree and returns
// deduplicated RelCalls relations plus per-function complexity metrics,
// used for function/method/arrow facts. selfName/selfShort enable direct-recursion
// detection.
func collectCallsWithMetrics(node *sitter.Node, src []byte, dir, className string, importMap map[string]string, selfName, selfShort string) ([]facts.Relation, *tsBodyMetrics) {
	m := &tsBodyMetrics{}
	w := &tsBodyWalker{src: src, dir: dir, className: className, importMap: importMap, selfName: selfName, selfShort: selfShort, metrics: m, seen: make(map[string]bool)}
	w.walk(node)
	return w.rels, m
}

// applyTSMetrics writes the complexity props onto a function/method fact's Props.
func applyTSMetrics(props map[string]any, m *tsBodyMetrics) {
	if m == nil {
		return
	}
	props["cyclomatic"] = 1 + m.decisions
	if m.loopDepth > 0 {
		props["loop_depth"] = m.loopDepth
	}
	if m.loopCount > 0 {
		props["loop_count"] = m.loopCount
	}
	if len(m.callsInLoop) > 0 {
		props["calls_in_loop"] = m.callsInLoop
	}
	if m.recursive {
		props["recursive_self"] = true
	}
}

// resolveTSCall resolves a single call_expression to a canonical target fact name,
// or "" when the call cannot be resolved (e.g. a method call on a value of unknown
// type). It resolves:
//   - bare calls `foo()` → imported symbol via importMap, else same-module "<dir>.foo"
//   - `this.method()` inside a class → "<dir>.<className>.method"
func resolveTSCall(call *sitter.Node, src []byte, dir, className string, importMap map[string]string) string {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return ""
	}
	switch fn.Kind() {
	case "identifier":
		name := nodeText(fn, src)
		if target, ok := importMap[name]; ok {
			return target
		}
		return dir + "." + name
	case "member_expression":
		object := fn.ChildByFieldName("object")
		property := fn.ChildByFieldName("property")
		if object == nil || property == nil {
			return ""
		}
		// `this.method()` resolves within the enclosing class; other receivers
		// have an unknown type and are left unresolved to avoid dangling edges.
		if object.Kind() == "this" && className != "" {
			return dir + "." + className + "." + nodeText(property, src)
		}
	}
	return ""
}
