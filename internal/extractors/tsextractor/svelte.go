package tsextractor

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"

	sitter "github.com/tree-sitter/go-tree-sitter"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// svelteScriptBlock holds the extracted <script> content from a Svelte SFC.
type svelteScriptBlock struct {
	Content   []byte
	IsModule  bool   // <script context="module"> (Svelte 4) or <script module> (Svelte 5)
	Lang      string // "ts", "tsx", or ""
	StartLine int
}

// extractSvelteScriptBlocks extracts all <script> blocks from a Svelte SFC.
// A Svelte file may have an instance script and a module script.
func extractSvelteScriptBlocks(src []byte) []*svelteScriptBlock {
	var blocks []*svelteScriptBlock
	pos := 0
	for {
		remaining := src[pos:]
		idx := indexCaseInsensitive(remaining, []byte("<script"))
		if idx < 0 {
			break
		}
		tagStart := pos + idx

		tagEnd := bytes.IndexByte(src[tagStart:], '>')
		if tagEnd < 0 {
			break
		}
		tagEnd += tagStart

		attrs := string(src[tagStart : tagEnd+1])

		closeIdx := indexCaseInsensitive(src[tagEnd+1:], []byte("</script>"))
		if closeIdx < 0 {
			break
		}
		closeIdx += tagEnd + 1

		content := src[tagEnd+1 : closeIdx]
		lower := strings.ToLower(attrs)
		isModule := strings.Contains(lower, "module") || strings.Contains(lower, `context="module"`)
		lang := extractAttr(attrs, "lang")
		startLine := bytes.Count(src[:tagEnd+1], []byte("\n"))

		blocks = append(blocks, &svelteScriptBlock{
			Content:   content,
			IsModule:  isModule,
			Lang:      lang,
			StartLine: startLine,
		})

		pos = closeIdx + len("</script>")
	}
	return blocks
}

func detectSvelte(repoPath string) bool {
	tsRoot, _ := findTSRoot(repoPath)
	return hasPkgDependency(tsRoot, "svelte") || (tsRoot != repoPath && hasPkgDependency(repoPath, "svelte"))
}

func detectSvelteKit(repoPath string) bool {
	tsRoot, _ := findTSRoot(repoPath)
	return detectSvelteKitAt(tsRoot) || (tsRoot != repoPath && detectSvelteKitAt(repoPath))
}

func detectSvelteKitAt(dir string) bool {
	for _, name := range []string{"svelte.config.js", "svelte.config.ts", "svelte.config.mjs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return hasPkgDependency(dir, "@sveltejs/kit")
}

// detectSvelteKitRoute checks if a file path corresponds to a SvelteKit route.
func detectSvelteKitRoute(relFile string) *facts.Fact {
	parts := strings.Split(filepath.ToSlash(relFile), "/")

	for i, p := range parts {
		if p == "routes" && i < len(parts)-1 {
			fileName := parts[len(parts)-1]
			ext := filepath.Ext(fileName)
			baseName := strings.TrimSuffix(fileName, ext)

			switch baseName {
			case "+page", "+layout", "+error":
				// Page/layout/error components
			case "+server":
				// API route handler
			case "+page.server", "+layout.server":
				// Server-side load functions — not routes themselves
				return nil
			default:
				return nil
			}

			segParts := parts[i+1 : len(parts)-1]
			urlParts := make([]string, 0, len(segParts))
			for _, seg := range segParts {
				if len(seg) >= 2 && seg[0] == '(' && seg[len(seg)-1] == ')' {
					continue
				}
				urlParts = append(urlParts, seg)
			}

			routePath := "/" + strings.Join(urlParts, "/")

			method := "GET"
			routeType := baseName[1:] // strip leading "+"
			if baseName == "+server" {
				method = "ALL"
			}

			return &facts.Fact{
				Kind: facts.KindRoute,
				Name: routePath,
				File: relFile,
				Line: 1,
				Props: map[string]any{
					"method":    method,
					"type":      routeType,
					"router":    "sveltekit",
					"language":  "typescript",
					"framework": "sveltekit",
				},
			}
		}
	}

	return nil
}

func isSvelteFile(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".svelte"
}

// svelteJSKeywords are excluded from markup identifier scanning — they can appear
// inside a mustache expression (`{#if x}`, `{x ? a : b}`) but never name a script
// symbol, so capturing them would be a wasted no-op at best.
var svelteJSKeywords = map[string]bool{
	"if": true, "else": true, "each": true, "as": true, "await": true, "then": true,
	"catch": true, "true": true, "false": true, "null": true, "undefined": true,
	"this": true, "new": true, "typeof": true, "in": true, "of": true, "async": true,
	"return": true, "const": true, "let": true, "var": true, "function": true,
	"void": true, "delete": true, "instanceof": true, "yield": true, "from": true,
}

var (
	svelteIdentRe     = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)
	svelteDirectiveRe = regexp.MustCompile(`\b(?:bind|use):([A-Za-z_$][A-Za-z0-9_$]*)`)
)

// findTagBlockSpans returns the byte ranges [start,end) of every `<tag ...>...
// </tag>` element (case-insensitive) in src, so callers can exclude them from a
// markup scan. Mirrors extractSvelteScriptBlocks' tag-finding but is reusable for
// any tag name (here: script and style).
func findTagBlockSpans(src []byte, tag string) [][2]int {
	var spans [][2]int
	pos := 0
	openTag := []byte("<" + tag)
	closeTag := []byte("</" + tag + ">")
	for {
		remaining := src[pos:]
		idx := indexCaseInsensitive(remaining, openTag)
		if idx < 0 {
			break
		}
		tagStart := pos + idx

		tagEnd := bytes.IndexByte(src[tagStart:], '>')
		if tagEnd < 0 {
			break
		}
		tagEnd += tagStart

		closeIdx := indexCaseInsensitive(src[tagEnd+1:], closeTag)
		if closeIdx < 0 {
			break
		}
		closeIdx += tagEnd + 1

		end := closeIdx + len(closeTag)
		spans = append(spans, [2]int{tagStart, end})
		pos = end
	}
	return spans
}

// svelteMarkupOnly returns the SFC source with every <script> and <style> block
// removed, leaving only the template/markup portion for reference scanning.
func svelteMarkupOnly(src []byte) []byte {
	spans := append(findTagBlockSpans(src, "script"), findTagBlockSpans(src, "style")...)
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })

	var out []byte
	pos := 0
	for _, sp := range spans {
		if sp[0] < pos {
			continue // overlapping/out-of-order tag match, skip rather than corrupt output
		}
		out = append(out, src[pos:sp[0]]...)
		pos = sp[1]
	}
	out = append(out, src[pos:]...)
	return out
}

// scanMustacheExpressions returns the raw byte content of every top-level {...}
// mustache expression in markup — event-handler attribute values (on:click={fn}),
// bind:/use: expression forms (bind:x={fn}), and text/attribute interpolations
// ({fn(x)}) are all syntactically a brace group at this level. It tracks brace
// depth and skips over '/" string literals so a brace inside a string literal
// can't unbalance the scan; backtick template literals are not string-skipped
// (so a nested ${...} is still walked as an expression, which is what we want),
// at the cost of not perfectly balancing a stray literal '{' or '}' inside one.
func scanMustacheExpressions(markup []byte) [][]byte {
	var exprs [][]byte
	depth := 0
	start := 0
	var quote byte
	for i := 0; i < len(markup); i++ {
		c := markup[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			if depth > 0 {
				quote = c
			}
		case '{':
			if depth == 0 {
				start = i + 1
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 {
					exprs = append(exprs, markup[start:i])
				}
			}
		}
	}
	return exprs
}

// extractSvelteMarkupRefs scans a Svelte SFC's template for identifiers referenced
// only from markup — event-handler attributes (on:click={fn}, onclick={fn}),
// mustache expressions ({fn()}), and bind:/use: directives — that the script-only
// AST walk in extractSvelteScriptBlock can never see (extractSvelteSFC only feeds
// <script> content to the parser; the template is otherwise discarded). It emits a
// single KindFileRef fact per file, exactly like the TS extractor's JSX file-ref
// pass (collectTSFileRefs in ts.go), so these references fold into find_orphans'
// usage graph downstream by short-name matching. The pass only ever ADDS
// references — it can hide a real orphan but never invent a false one.
func extractSvelteMarkupRefs(rawSrc []byte, relFile string) *facts.Fact {
	markup := svelteMarkupOnly(rawSrc)

	seen := make(map[string]bool)
	var targets []string
	add := func(name string) {
		if name == "" || svelteJSKeywords[name] || seen[name] {
			return
		}
		seen[name] = true
		targets = append(targets, name)
	}

	for _, expr := range scanMustacheExpressions(markup) {
		for _, m := range svelteIdentRe.FindAll(expr, -1) {
			add(string(m))
		}
	}
	for _, m := range svelteDirectiveRe.FindAllSubmatch(markup, -1) {
		add(string(m[1]))
	}

	if len(targets) == 0 {
		return nil
	}

	rels := make([]facts.Relation, 0, len(targets))
	for _, t := range targets {
		rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: t})
	}
	return &facts.Fact{
		Kind:      facts.KindFileRef,
		Name:      relFile,
		File:      relFile,
		Line:      1,
		Props:     map[string]any{"language": "typescript"},
		Relations: rels,
	}
}

// extractSvelteSFC extracts architectural facts from a Svelte Single File Component.
func (e *TSExtractor) extractSvelteSFC(rawSrc []byte, relFile string, isSvelteKit bool, aliases map[string]string) []facts.Fact {
	var result []facts.Fact
	blocks := extractSvelteScriptBlocks(rawSrc)

	for _, block := range blocks {
		result = append(result, e.extractSvelteScriptBlock(block, relFile, isSvelteKit, aliases)...)
	}

	if ref := extractSvelteMarkupRefs(rawSrc, relFile); ref != nil {
		result = append(result, *ref)
	}

	dir := filepath.Dir(relFile)
	componentName := fileSymbolName(relFile)
	factName := dir + "." + componentName
	fw := "svelte"
	if isSvelteKit {
		fw = "sveltekit"
	}

	found := false
	for i := range result {
		if result[i].Kind == facts.KindSymbol && result[i].Name == factName {
			result[i].Props["web_component"] = "component"
			result[i].Props["framework"] = fw
			found = true
			break
		}
	}
	if !found {
		result = append(result, facts.Fact{
			Kind: facts.KindSymbol,
			Name: factName,
			File: relFile,
			Line: 1,
			Props: map[string]any{
				"symbol_kind":   facts.SymbolFunc,
				"exported":      true,
				"language":      "typescript",
				"web_component": "component",
				"framework":     fw,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}

	if isSvelteKit {
		if routeFact := detectSvelteKitRoute(relFile); routeFact != nil {
			result = append(result, *routeFact)
		}
	}

	return result
}

func (e *TSExtractor) extractSvelteScriptBlock(block *svelteScriptBlock, relFile string, isSvelteKit bool, aliases map[string]string) []facts.Fact {
	isTSX := block.Lang == "tsx"
	lang := typescript.LanguageTypescript()
	if isTSX {
		lang = typescript.LanguageTSX()
	}

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(lang)); err != nil {
		return nil
	}

	tree := parser.Parse(block.Content, nil)
	defer tree.Close()

	root := tree.RootNode()

	var result []facts.Fact
	result = append(result, e.extractImports(root, block.Content, relFile, aliases)...)

	ctx := &extractCtx{
		src:       block.Content,
		relFile:   relFile,
		dir:       filepath.Dir(relFile),
		isTSX:     isTSX,
		importMap: buildImportSymbols(root, block.Content, relFile, aliases),
	}
	decls := e.extractDeclarations(root, ctx)

	if exported := collectExportedLocalNames(root, block.Content); len(exported) > 0 {
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

	if block.StartLine > 0 {
		for i := range result {
			if result[i].Line > 0 {
				result[i].Line += block.StartLine
			}
		}
	}

	return result
}
