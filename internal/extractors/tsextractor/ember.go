package tsextractor

import (
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/enola-labs/enola/internal/facts"
)

// Ember/Glimmer support — the third framework dialect the TypeScript extractor
// handles beside Vue and Svelte.
//
// Ember is the inverse slicing problem: where a Vue SFC is markup with embedded
// <script> blocks, a Glimmer template-tag file (.gts/.gjs) is TypeScript/JavaScript
// with embedded <template> blocks. Rather than adjusting line offsets the way the
// Vue path must, the template spans are blanked in place — every byte except
// newlines replaced with spaces — so the remainder parses with the standard grammar
// and every fact's Line is true to the original file by construction.
//
// Three resolution regimes, in decreasing order of certainty:
//
//   - .gts/.gjs templates are strict-mode: anything a template renders must be in
//     scope, which for components/helpers means imported. Template identifier
//     tokens are matched against the file's own import bindings; a token that is
//     not an import is a local (an @arg, a block param, `this.*`) and produces no
//     edge.
//   - `@service` fields name a service by convention. The extractor records the
//     names; the ember-resolver binder resolves them against the store, where the
//     actual declared class symbol is known.
//   - .hbs invocations are bare strings with no import to anchor on. The extractor
//     records them on a file_ref carrier; the binder resolves via Ember's default
//     resolver layout (components/, helpers/) and skips anything ambiguous —
//     a missing edge beats a wrong one.

// EmberFramework is the framework prop value stamped on Ember facts.
const EmberFramework = "ember"

// emberTemplateOpen/Close delimit a Glimmer template block. Glimmer requires the
// lowercase form; anything else is ordinary markup in a string and is not sliced.
const (
	emberTemplateOpen  = "<template>"
	emberTemplateClose = "</template>"
)

func isEmberTemplateTagFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".gts" || ext == ".gjs"
}

func isHbsFile(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".hbs"
}

// detectEmber reports whether the repo declares ember-source, reusing the same
// package.json primitive (and tsRoot + repo-root fallback) as Vue/Nuxt/ORM
// detection.
func detectEmber(repoPath string) bool {
	tsRoot, _ := findTSRoot(repoPath)
	return hasPkgDependency(tsRoot, "ember-source") ||
		(tsRoot != repoPath && hasPkgDependency(repoPath, "ember-source"))
}

// emberTemplateSegment is one <template> block sliced out of a .gts/.gjs file.
type emberTemplateSegment struct {
	Content string
}

// blankEmberTemplates replaces every <template>…</template> span (tags included)
// with spaces, preserving newlines so tree-sitter positions in the remainder match
// the original file exactly. Returns the blanked source and the sliced-out
// template contents. An unclosed <template> is left untouched: half a blank would
// corrupt the parse worse than an unparsed template block.
func blankEmberTemplates(src []byte) ([]byte, []emberTemplateSegment) {
	var segments []emberTemplateSegment
	out := make([]byte, len(src))
	copy(out, src)
	pos := 0
	for {
		idx := strings.Index(string(out[pos:]), emberTemplateOpen)
		if idx < 0 {
			break
		}
		start := pos + idx
		closeIdx := strings.Index(string(out[start:]), emberTemplateClose)
		if closeIdx < 0 {
			break
		}
		end := start + closeIdx + len(emberTemplateClose)
		inner := string(src[start+len(emberTemplateOpen) : start+closeIdx])
		segments = append(segments, emberTemplateSegment{Content: inner})
		for i := start; i < end; i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
		pos = end
	}
	return out, segments
}

// emberImportBindings maps each locally-bound import name to its resolution:
// internal imports to a canonical symbol name ("<moduleDir>.<localName>"),
// external imports to their module specifier. Default and named imports both
// bind — Ember components are overwhelmingly default exports, which is exactly
// the case buildImportSymbols skips for the general call-resolution path.
type emberImportBindings struct {
	internal map[string]string
	external map[string]string
}

func buildEmberImportBindings(root *sitter.Node, src []byte, relFile string, aliases map[string]string) emberImportBindings {
	b := emberImportBindings{
		internal: make(map[string]string),
		external: make(map[string]string),
	}
	fileDir := filepath.Dir(relFile)
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
		moduleDir := filepath.ToSlash(filepath.Dir(resolved))

		clause := findChildByKind(child, "import_clause")
		if clause == nil {
			continue
		}
		bind := func(local string) {
			if local == "" {
				return
			}
			if isExternal {
				b.external[local] = importPath
			} else {
				b.internal[local] = moduleDir + "." + local
			}
		}
		for j := range clause.ChildCount() {
			c := clause.Child(j)
			switch c.Kind() {
			case "identifier":
				bind(nodeText(c, src))
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
					local := nodeText(nameNode, src)
					if a := spec.ChildByFieldName("alias"); a != nil {
						local = nodeText(a, src)
					}
					bind(local)
				}
			}
		}
	}
	return b
}

// emberTemplateRefs returns the canonical targets of every identifier token in the
// template segments that matches an internal import binding, sorted and deduplicated.
// The strict-mode invariant does the filtering: locals, @args, block params and
// built-in keywords are simply never in the binding map.
func emberTemplateRefs(segments []emberTemplateSegment, bindings emberImportBindings) []string {
	seen := make(map[string]bool)
	for _, seg := range segments {
		for _, tok := range identTokens(seg.Content) {
			if target, ok := bindings.internal[tok]; ok {
				seen[target] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	targets := make([]string, 0, len(seen))
	for t := range seen {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	return targets
}

// identTokens splits text into identifier-shaped tokens (letters, digits, _, $).
func identTokens(text string) []string {
	var tokens []string
	start := -1
	isIdent := func(r byte) bool {
		return r == '_' || r == '$' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	for i := 0; i <= len(text); i++ {
		if i < len(text) && isIdent(text[i]) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			tokens = append(tokens, text[start:i])
			start = -1
		}
	}
	return tokens
}

// dasherize converts CamelCase/PascalCase to kebab-case ("AboardApollo" →
// "aboard-apollo"), matching Ember's name convention for services and models.
func dasherize(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// emberModuleBindings are the external modules whose default exports mark a class
// as an Ember construct when extended.
var (
	emberComponentModules = map[string]bool{"@glimmer/component": true, "@ember/component": true}
	emberModelModules     = map[string]bool{"@ember-data/model": true, "ember-data": true}
	emberServiceModules   = map[string]bool{"@ember/service": true}
)

// emberClassInfo describes one top-level class declaration and the Ember role its
// heritage and decorators imply.
type emberClassInfo struct {
	name        string
	line        int
	isComponent bool
	isModel     bool
	isService   bool
	services    []string
}

// collectEmberClasses walks the top-level declarations (unwrapping export
// statements) and classifies each class by what its superclass's local name was
// imported from, plus the service names its @service-decorated fields inject.
func collectEmberClasses(root *sitter.Node, src []byte, bindings emberImportBindings) []emberClassInfo {
	serviceDecorators := emberServiceDecoratorNames(bindings)
	var classes []emberClassInfo
	for i := range root.ChildCount() {
		node := root.Child(i)
		if node.Kind() == "export_statement" {
			if decl := firstDeclChild(node); decl != nil {
				node = decl
			} else if c := findChildByKind(node, "class"); c != nil {
				node = c
			}
		}
		switch node.Kind() {
		case "class_declaration", "abstract_class_declaration", "class":
		default:
			continue
		}
		info := emberClassInfo{line: int(node.StartPosition().Row) + 1}
		if name := findChildByKind(node, "type_identifier"); name != nil {
			info.name = nodeText(name, src)
		}
		if super := emberSuperclassName(node, src); super != "" {
			if mod, ok := bindings.external[super]; ok {
				info.isComponent = emberComponentModules[mod]
				info.isModel = emberModelModules[mod]
				info.isService = emberServiceModules[mod]
			}
		}
		if body := findChildByKind(node, "class_body"); body != nil {
			info.services = emberInjectedServices(body, src, serviceDecorators)
		}
		classes = append(classes, info)
	}
	return classes
}

// emberServiceDecoratorNames returns the local names bound to the service
// decorator export of @ember/service (`service`, and the legacy `inject`).
func emberServiceDecoratorNames(bindings emberImportBindings) map[string]bool {
	names := make(map[string]bool)
	for local, mod := range bindings.external {
		if emberServiceModules[mod] {
			names[local] = true
		}
	}
	return names
}

func emberSuperclassName(classNode *sitter.Node, src []byte) string {
	for i := range classNode.ChildCount() {
		c := classNode.Child(i)
		if c.Kind() != "class_heritage" {
			continue
		}
		for j := range c.ChildCount() {
			h := c.Child(j)
			if h.Kind() == "extends_clause" {
				for k := range h.ChildCount() {
					t := h.Child(k)
					if t.Kind() == "identifier" {
						return nodeText(t, src)
					}
				}
			}
		}
		if id := findChildByKind(c, "identifier"); id != nil {
			return nodeText(id, src)
		}
	}
	return ""
}

// emberInjectedServices reads @service-decorated class fields. `@service store`
// injects the service named by the field (camelCase dasherized); `@service('a/b')`
// injects the named path. Names are sorted for deterministic output.
func emberInjectedServices(classBody *sitter.Node, src []byte, decorators map[string]bool) []string {
	if len(decorators) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	for i := range classBody.ChildCount() {
		member := classBody.Child(i)
		if member.Kind() != "public_field_definition" && member.Kind() != "field_definition" {
			continue
		}
		for j := range member.ChildCount() {
			dec := member.Child(j)
			if dec.Kind() != "decorator" {
				continue
			}
			name, arg := emberDecoratorNameArg(dec, src)
			if !decorators[name] {
				continue
			}
			if arg == "" {
				if field := emberFieldName(member, src); field != "" {
					arg = dasherize(field)
				}
			}
			if arg != "" {
				seen[arg] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func emberDecoratorNameArg(dec *sitter.Node, src []byte) (name, arg string) {
	for i := range dec.ChildCount() {
		c := dec.Child(i)
		switch c.Kind() {
		case "identifier":
			return nodeText(c, src), ""
		case "call_expression":
			fn := c.ChildByFieldName("function")
			if fn == nil || fn.Kind() != "identifier" {
				return "", ""
			}
			if args := c.ChildByFieldName("arguments"); args != nil {
				if s := findChildByKind(args, "string"); s != nil {
					return nodeText(fn, src), strings.Trim(nodeText(s, src), `"'`)
				}
			}
			return nodeText(fn, src), ""
		}
	}
	return "", ""
}

func emberFieldName(member *sitter.Node, src []byte) string {
	if n := member.ChildByFieldName("name"); n != nil {
		return nodeText(n, src)
	}
	if n := findChildByKind(member, "property_identifier"); n != nil {
		return nodeText(n, src)
	}
	return ""
}

// EmberServicesProp carries the sorted service names a class injects; the
// ember-resolver binder resolves them to the declared service class symbols.
const EmberServicesProp = "ember_injected_services"

// emberEnrich applies Ember classification to the already-extracted declaration
// facts of one file: component/service classes gain their props, template
// references attach to the component symbol as calls, ember-data models gain a
// companion storage fact, and a template-only .gts/.gjs synthesizes its component
// symbol the same way a script-less Vue SFC does.
func emberEnrich(result []facts.Fact, root *sitter.Node, src []byte, relFile string,
	aliases map[string]string, segments []emberTemplateSegment) []facts.Fact {

	dir := filepath.Dir(relFile)
	bindings := buildEmberImportBindings(root, src, relFile, aliases)
	classes := collectEmberClasses(root, src, bindings)
	templateRefs := emberTemplateRefs(segments, bindings)

	componentAttached := false
	for _, cls := range classes {
		if cls.name == "" {
			continue
		}
		factName := dir + "." + cls.name
		for i := range result {
			if result[i].Kind != facts.KindSymbol || result[i].Name != factName {
				continue
			}
			if cls.isComponent {
				result[i].Props["web_component"] = "component"
				result[i].Props["framework"] = EmberFramework
				if !componentAttached {
					for _, t := range templateRefs {
						if !result[i].HasRelation(facts.RelCalls, t) {
							result[i].Relations = append(result[i].Relations,
								facts.Relation{Kind: facts.RelCalls, Target: t})
						}
					}
					componentAttached = true
				}
			}
			if cls.isService {
				base := strings.TrimSuffix(filepath.Base(relFile), filepath.Ext(relFile))
				result[i].Props["ember_service"] = base
			}
			if len(cls.services) > 0 {
				result[i].Props[EmberServicesProp] = cls.services
			}
			break
		}
		if cls.isModel {
			base := strings.TrimSuffix(filepath.Base(relFile), filepath.Ext(relFile))
			result = append(result, facts.Fact{
				Kind: facts.KindStorage,
				Name: factName,
				File: relFile,
				Line: cls.line,
				Props: map[string]any{
					"storage_kind": "model",
					"framework":    "ember-data",
					"table":        base,
					"language":     "typescript",
				},
				Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
			})
		}
	}

	if !componentAttached && len(segments) > 0 && isEmberTemplateTagFile(relFile) {
		rels := []facts.Relation{{Kind: facts.RelDeclares, Target: dir}}
		for _, t := range templateRefs {
			rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: t})
		}
		result = append(result, facts.Fact{
			Kind: facts.KindSymbol,
			Name: dir + "." + fileSymbolName(relFile),
			File: relFile,
			Line: 1,
			Props: map[string]any{
				"symbol_kind":   facts.SymbolFunc,
				"exported":      true,
				"language":      "typescript",
				"web_component": "component",
				"framework":     EmberFramework,
			},
			Relations: rels,
		})
	}

	return result
}

// EmberTemplateProp marks the file_ref carrier an .hbs template emits;
// EmberInvocationsProp holds the sorted invocation names found in it, and
// EmberOwnerFileProp the co-located class file when one exists. The
// ember-resolver binder consumes all three.
const (
	EmberTemplateProp    = "ember_template"
	EmberInvocationsProp = "ember_invocations"
	EmberOwnerFileProp   = "ember_owner_file"
)

// emberHbsKeywords are Glimmer's built-in helpers and keywords: never component
// invocations, so never candidates for resolution.
var emberHbsKeywords = map[string]bool{
	"if": true, "unless": true, "each": true, "each-in": true, "let": true,
	"yield": true, "outlet": true, "component": true, "helper": true,
	"modifier": true, "action": true, "fn": true, "on": true, "get": true,
	"concat": true, "array": true, "hash": true, "debugger": true, "log": true,
	"mount": true, "input": true, "textarea": true, "link-to": true,
	"query-params": true, "unbound": true, "mut": true, "in-element": true,
	"has-block": true, "has-block-params": true, "else": true,
}

// extractEmberHbs models one classic .hbs template. The invocation names it
// records are candidates; resolution happens in the ember-resolver binder where
// the whole store is visible. A component template with no co-located class is a
// template-only component and synthesizes its component symbol here, since a
// binder may never add facts.
func (e *TSExtractor) extractEmberHbs(src []byte, relFile string, knownFiles map[string]bool) []facts.Fact {
	dir := filepath.Dir(relFile)
	invocations := scanHbsInvocations(string(src))

	ownerFile := ""
	base := strings.TrimSuffix(relFile, filepath.Ext(relFile))
	for _, ext := range []string{".ts", ".js", ".gts", ".gjs"} {
		if knownFiles[filepath.ToSlash(base+ext)] {
			ownerFile = filepath.ToSlash(base + ext)
			break
		}
	}

	var result []facts.Fact
	isComponentTemplate := strings.Contains(filepath.ToSlash(relFile), "/components/")
	if ownerFile == "" && isComponentTemplate {
		result = append(result, facts.Fact{
			Kind: facts.KindSymbol,
			Name: dir + "." + fileSymbolName(relFile),
			File: relFile,
			Line: 1,
			Props: map[string]any{
				"symbol_kind":   facts.SymbolFunc,
				"exported":      true,
				"language":      "handlebars",
				"web_component": "component",
				"framework":     EmberFramework,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}

	props := map[string]any{
		"language":        "handlebars",
		EmberTemplateProp: true,
	}
	if len(invocations) > 0 {
		props[EmberInvocationsProp] = invocations
	}
	if ownerFile != "" {
		props[EmberOwnerFileProp] = ownerFile
	}
	result = append(result, facts.Fact{
		Kind:  facts.KindFileRef,
		Name:  relFile,
		File:  relFile,
		Line:  1,
		Props: props,
	})
	return result
}

// scanHbsInvocations collects component/helper invocation names from template
// text: angle-bracket tags starting with an uppercase letter (`<CoreModal`,
// `<Ui::Button`) and mustache/subexpression heads in kebab-case with at least one
// hyphen (`{{format-date …}}`, `(errors-for …)`). The hyphen requirement on curly
// names is the determinism line: a bare `{{title}}` is indistinguishable from a
// property, while a hyphenated name cannot be one.
func scanHbsInvocations(text string) []string {
	seen := make(map[string]bool)
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '<':
			j := i + 1
			if j < len(text) && text[j] >= 'A' && text[j] <= 'Z' {
				k := j
				for k < len(text) && (isHbsNameByte(text[k]) || text[k] == ':') {
					k++
				}
				seen[strings.TrimRight(text[j:k], ":")] = true
				i = k
			}
		case '{', '(':
			j := i
			if text[i] == '{' {
				if i+1 >= len(text) || text[i+1] != '{' {
					continue
				}
				j = i + 2
			} else {
				j = i + 1
			}
			for j < len(text) && (text[j] == '#' || text[j] == ' ') {
				j++
			}
			k := j
			for k < len(text) && isHbsNameByte(text[k]) {
				k++
			}
			name := text[j:k]
			if strings.Contains(name, "-") && !emberHbsKeywords[name] &&
				name != "" && name[0] >= 'a' && name[0] <= 'z' {
				seen[name] = true
			}
			i = k
		}
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func isHbsNameByte(b byte) bool {
	return b == '-' || b == '_' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// isEmberRouterFile reports whether relFile is the app's router map.
func isEmberRouterFile(relFile string) bool {
	base := filepath.Base(relFile)
	return base == "router.ts" || base == "router.js"
}

// extractEmberRoutes walks Router.map's `this.route(name, {path}, fn)` DSL and
// emits one client-side page route per declaration, with parent paths composed
// the way Ember's router composes them. These are UI routes, not HTTP contracts —
// the same modelling Nuxt pages and SvelteKit routes already get.
func extractEmberRoutes(root *sitter.Node, src []byte, relFile string) []facts.Fact {
	var result []facts.Fact
	var walk func(n *sitter.Node, prefix, namePrefix string)
	walk = func(n *sitter.Node, prefix, namePrefix string) {
		if n == nil {
			return
		}
		if n.Kind() == "call_expression" {
			if fn := n.ChildByFieldName("function"); fn != nil &&
				fn.Kind() == "member_expression" && nodeText(fn, src) == "this.route" {
				name, path, callback := emberRouteArgs(n, src)
				if name != "" {
					full := joinEmberPath(prefix, path)
					routeName := name
					if namePrefix != "" {
						routeName = namePrefix + "." + name
					}
					result = append(result, facts.Fact{
						Kind: facts.KindRoute,
						Name: full,
						File: relFile,
						Line: int(n.StartPosition().Row) + 1,
						Props: map[string]any{
							"method":           "GET",
							"type":             "page",
							"router":           "map",
							"language":         "typescript",
							"framework":        EmberFramework,
							"ember_route_name": routeName,
						},
					})
					if callback != nil {
						walk(callback, full, routeName)
						return
					}
					return
				}
			}
		}
		for i := range n.ChildCount() {
			walk(n.Child(i), prefix, namePrefix)
		}
	}
	walk(root, "", "")
	return result
}

// emberRouteArgs pulls (name, path, nested-callback) out of one this.route call.
// path defaults to the route name when no {path: …} option is given, matching the
// router's own default.
func emberRouteArgs(call *sitter.Node, src []byte) (name, path string, callback *sitter.Node) {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return "", "", nil
	}
	for i := range args.ChildCount() {
		a := args.Child(i)
		switch a.Kind() {
		case "string":
			if name == "" {
				name = strings.Trim(nodeText(a, src), `"'`)
			}
		case "object":
			for j := range a.ChildCount() {
				pair := a.Child(j)
				if pair.Kind() != "pair" {
					continue
				}
				key := pair.ChildByFieldName("key")
				val := pair.ChildByFieldName("value")
				if key != nil && val != nil && nodeText(key, src) == "path" && val.Kind() == "string" {
					path = strings.Trim(nodeText(val, src), `"'`)
				}
			}
		case "function_expression", "arrow_function":
			callback = a
		}
	}
	if path == "" {
		path = name
	}
	return name, path, callback
}

func joinEmberPath(prefix, path string) string {
	path = strings.TrimPrefix(path, "/")
	prefix = strings.TrimSuffix(prefix, "/")
	if path == "" {
		if prefix == "" {
			return "/"
		}
		return prefix
	}
	if prefix == "" {
		return "/" + path
	}
	return prefix + "/" + path
}
