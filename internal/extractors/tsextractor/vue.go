package tsextractor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"

	sitter "github.com/tree-sitter/go-tree-sitter"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// vueScriptBlock holds the extracted <script> content from a Vue SFC.
type vueScriptBlock struct {
	Content   []byte
	IsSetup   bool
	Lang      string // "ts", "tsx", "js", or ""
	StartLine int    // 0-based line number where the script content begins
}

// extractVueScriptBlocks extracts all <script> blocks from a Vue SFC file.
// A Vue SFC may contain both a <script> and a <script setup> block.
func extractVueScriptBlocks(src []byte) []*vueScriptBlock {
	var blocks []*vueScriptBlock
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
		isSetup := strings.Contains(strings.ToLower(attrs), "setup")
		lang := extractAttr(attrs, "lang")
		startLine := bytes.Count(src[:tagEnd+1], []byte("\n"))

		blocks = append(blocks, &vueScriptBlock{
			Content:   content,
			IsSetup:   isSetup,
			Lang:      lang,
			StartLine: startLine,
		})

		pos = closeIdx + len("</script>")
	}
	return blocks
}

func extractAttr(tag, name string) string {
	for _, q := range []byte{'"', '\''} {
		needle := name + "=" + string(q)
		idx := strings.Index(strings.ToLower(tag), strings.ToLower(needle))
		if idx < 0 {
			continue
		}
		start := idx + len(needle)
		end := strings.IndexByte(tag[start:], q)
		if end < 0 {
			continue
		}
		return tag[start : start+end]
	}
	return ""
}

func indexCaseInsensitive(src, needle []byte) int {
	return bytes.Index(bytes.ToLower(src), bytes.ToLower(needle))
}

func detectVue(repoPath string) bool {
	tsRoot, _ := findTSRoot(repoPath)
	return detectVueAt(tsRoot) || (tsRoot != repoPath && detectVueAt(repoPath))
}

func detectVueAt(dir string) bool {
	return hasPkgDependency(dir, "vue")
}

func detectNuxt(repoPath string) bool {
	tsRoot, _ := findTSRoot(repoPath)
	return detectNuxtAt(tsRoot) || (tsRoot != repoPath && detectNuxtAt(repoPath))
}

func detectNuxtAt(dir string) bool {
	for _, name := range []string{"nuxt.config.js", "nuxt.config.ts", "nuxt.config.mjs"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return hasPkgDependency(dir, "nuxt")
}

func hasPkgDependency(dir, pkg string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var p map[string]any
	if err := json.Unmarshal(data, &p); err != nil {
		return false
	}
	for _, key := range []string{"dependencies", "devDependencies"} {
		if deps, ok := p[key].(map[string]any); ok {
			if _, ok := deps[pkg]; ok {
				return true
			}
		}
	}
	return false
}

// detectNuxtRoute checks if a .vue file path corresponds to a Nuxt route.
func detectNuxtRoute(relFile string) *facts.Fact {
	if !strings.HasSuffix(relFile, ".vue") {
		return nil
	}

	parts := strings.Split(filepath.ToSlash(relFile), "/")

	for i, p := range parts {
		if p == "pages" && i < len(parts)-1 {
			remaining := parts[i+1:]
			fileName := remaining[len(remaining)-1]
			baseName := strings.TrimSuffix(fileName, ".vue")

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

			return &facts.Fact{
				Kind: facts.KindRoute,
				Name: routePath,
				File: relFile,
				Line: 1,
				Props: map[string]any{
					"method":    "GET",
					"type":      "page",
					"router":    "pages",
					"language":  "typescript",
					"framework": "nuxt",
				},
			}
		}
	}

	return nil
}

func isVueFile(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".vue"
}

// containsCreateRouterCall reports whether the AST subtree contains a createRouter() call.
func containsCreateRouterCall(node *sitter.Node, src []byte) bool {
	if node == nil {
		return false
	}
	if node.Kind() == "call_expression" {
		fn := node.ChildByFieldName("function")
		if fn != nil && fn.Kind() == "identifier" && nodeText(fn, src) == "createRouter" {
			return true
		}
	}
	for i := range node.ChildCount() {
		if containsCreateRouterCall(node.Child(i), src) {
			return true
		}
	}
	return false
}

// extractVueSFC extracts architectural facts from a Vue Single File Component.
func (e *TSExtractor) extractVueSFC(rawSrc []byte, relFile string, isNuxt bool, aliases map[string]string) []facts.Fact {
	var result []facts.Fact
	blocks := extractVueScriptBlocks(rawSrc)

	isSetup := false
	for _, block := range blocks {
		if block.IsSetup {
			isSetup = true
		}
		result = append(result, e.extractVueScriptBlock(block, relFile, isNuxt, aliases)...)
	}

	dir := filepath.Dir(relFile)
	componentName := fileSymbolName(relFile)
	factName := dir + "." + componentName
	fw := "vue"
	if isNuxt {
		fw = "nuxt"
	}

	// If the script block already emitted a symbol with the component name
	// (e.g. from `export default defineComponent(...)`), enrich it with Vue
	// classification instead of creating a duplicate.
	found := false
	for i := range result {
		if result[i].Kind == facts.KindSymbol && result[i].Name == factName {
			result[i].Props["web_component"] = "component"
			result[i].Props["framework"] = fw
			if isSetup {
				result[i].Props["vue_setup"] = true
			}
			found = true
			break
		}
	}
	if !found {
		compFact := facts.Fact{
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
		}
		if isSetup {
			compFact.Props["vue_setup"] = true
		}
		result = append(result, compFact)
	}

	if isNuxt {
		if routeFact := detectNuxtRoute(relFile); routeFact != nil {
			result = append(result, *routeFact)
		}
	}

	return result
}

// extractVueScriptBlock parses a single <script> block from a Vue SFC and
// returns the extracted facts with line numbers adjusted to the original file.
func (e *TSExtractor) extractVueScriptBlock(block *vueScriptBlock, relFile string, isNuxt bool, aliases map[string]string) []facts.Fact {
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
		isVue:     true,
		isNuxt:    isNuxt,
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
