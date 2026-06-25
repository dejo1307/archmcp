package tsextractor

import (
	"bytes"
	"os"
	"path/filepath"
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

// extractSvelteSFC extracts architectural facts from a Svelte Single File Component.
func (e *TSExtractor) extractSvelteSFC(rawSrc []byte, relFile string, isSvelteKit bool, aliases map[string]string) []facts.Fact {
	var result []facts.Fact
	blocks := extractSvelteScriptBlocks(rawSrc)

	for _, block := range blocks {
		result = append(result, e.extractSvelteScriptBlock(block, relFile, isSvelteKit, aliases)...)
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
	parser.SetLanguage(sitter.NewLanguage(lang))

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
