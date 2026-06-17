package javaextractor

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/enola-labs/enola/internal/facts"
	sitter "github.com/tree-sitter/go-tree-sitter"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
)

// extractFileAST parses a single Java file with tree-sitter and emits architectural
// facts: declaration symbols (classes, interfaces, enums, records, methods, fields),
// import dependencies, and call-graph relations (RelImplements, RelInstantiates,
// RelInjects, RelCalls).
//
// Relation targets for type references (implements/instantiates/injects) are emitted
// as fully-qualified names — resolved through the file's import map, or assumed to be
// same-package when no import matches. java.go's canonicalizeTargets rewrites those
// FQNs to canonical "<dir>.<Type>" fact names once every file has been indexed.
func extractFileAST(src []byte, relFile string) []facts.Fact {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(java.Language())); err != nil {
		return nil
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil
	}
	defer tree.Close()

	w := &astWalker{
		src:       src,
		relFile:   relFile,
		dir:       filepath.Dir(relFile),
		importMap: make(map[string]string),
	}
	root := tree.RootNode()
	w.pkg = w.findPackage(root)
	w.walkProgram(root)
	return w.out
}

type astWalker struct {
	src     []byte
	relFile string
	dir     string
	pkg     string // dotted package name, e.g. "com.example.auth" ("" if none)

	out []facts.Fact

	// importMap maps an imported simple type name to its fully-qualified name
	// (e.g. "Store" -> "com.example.data.Store"). Used to resolve bare type
	// references in supertypes, constructor calls, and injected parameters.
	importMap map[string]string

	// typeStack holds the simple names of the enclosing type declarations, so a
	// method declared in class Foo is named "<dir>.Foo.method". methodStack is
	// parallel and holds the method-name set of each enclosing type, used to
	// resolve same-class bare calls.
	typeStack   []string
	methodStack []map[string]bool

	// ownerStack[len-1] is the symbol fact currently being built; call-graph edges
	// discovered while walking its body attach to it.
	ownerStack []*facts.Fact

	// routeStack is parallel to typeStack: each entry carries the Spring route
	// context (whether the enclosing type is a @Controller/@RestController and its
	// class-level base path) so method handlers can emit route facts.
	routeStack []routeScope
}

type routeScope struct {
	isController bool
	basePath     string
}

func (w *astWalker) enclosingType() string { return strings.Join(w.typeStack, ".") }

func (w *astWalker) qualify(name string) string {
	if t := w.enclosingType(); t != "" {
		return t + "." + name
	}
	return name
}

func (w *astWalker) currentMethods() map[string]bool {
	if len(w.methodStack) == 0 {
		return nil
	}
	return w.methodStack[len(w.methodStack)-1]
}

func (w *astWalker) currentRoute() *routeScope {
	if len(w.routeStack) == 0 {
		return nil
	}
	return &w.routeStack[len(w.routeStack)-1]
}

func (w *astWalker) pushOwner(f *facts.Fact) { w.ownerStack = append(w.ownerStack, f) }
func (w *astWalker) popOwner()               { w.ownerStack = w.ownerStack[:len(w.ownerStack)-1] }
func (w *astWalker) currentOwner() *facts.Fact {
	if len(w.ownerStack) == 0 {
		return nil
	}
	return w.ownerStack[len(w.ownerStack)-1]
}

// canonicalName is the "<dir>.<QualifiedType>" fact name of a declaration.
func (w *astWalker) canonicalName(qualified string) string { return w.dir + "." + qualified }

// fqn is the fully-qualified "<package>.<QualifiedType>" name of a declaration.
func (w *astWalker) fqn(qualified string) string {
	if w.pkg == "" {
		return qualified
	}
	return w.pkg + "." + qualified
}

func (w *astWalker) findPackage(root *sitter.Node) string {
	if pd := findChildByKind(root, "package_declaration"); pd != nil {
		// The package name is the scoped_identifier / identifier child.
		for i := uint(0); i < uint(pd.ChildCount()); i++ {
			c := pd.Child(i)
			if c.Kind() == "scoped_identifier" || c.Kind() == "identifier" {
				return nodeText(c, w.src)
			}
		}
	}
	return ""
}

func (w *astWalker) walkProgram(root *sitter.Node) {
	for i := uint(0); i < uint(root.ChildCount()); i++ {
		w.walkTopLevel(root.Child(i))
	}
}

func (w *astWalker) walkTopLevel(node *sitter.Node) {
	switch node.Kind() {
	case "import_declaration":
		w.handleImport(node)
	case "class_declaration":
		w.handleClassLike(node, facts.SymbolClass)
	case "interface_declaration":
		w.handleClassLike(node, facts.SymbolInterface)
	case "enum_declaration":
		w.handleClassLike(node, facts.SymbolEnum)
	case "record_declaration":
		w.handleClassLike(node, facts.SymbolClass)
	case "annotation_type_declaration":
		w.handleClassLike(node, facts.SymbolInterface)
	}
}

func (w *astWalker) handleImport(node *sitter.Node) {
	isStatic := false
	isWildcard := false
	var pathNode *sitter.Node
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		switch c.Kind() {
		case "static":
			isStatic = true
		case "asterisk":
			isWildcard = true
		case "scoped_identifier", "identifier":
			pathNode = c
		}
	}
	if pathNode == nil {
		return
	}
	importPath := nodeText(pathNode, w.src)

	props := map[string]any{
		"language": "java",
		"import":   importPath,
		"source":   "external", // refined to "internal" in canonicalizeTargets
	}
	// Mark the import shape so resolveImport can apply the parent-FQN fallback to
	// static-member / un-indexed-type imports but NOT to wildcards (whose import
	// string is already the package — walking to the grandparent would mis-resolve).
	if isStatic {
		props["static"] = true
	}
	if isWildcard {
		props["wildcard"] = true
	}

	w.out = append(w.out, facts.Fact{
		Kind:  facts.KindDependency,
		Name:  w.dir + " -> " + importPath,
		File:  w.relFile,
		Line:  int(node.StartPosition().Row) + 1,
		Props: props,
		Relations: []facts.Relation{
			{Kind: facts.RelImports, Target: importPath},
		},
	})

	// Record a non-static, non-wildcard import's simple name so bare type
	// references resolve to its FQN. Static imports name a member, not a type;
	// wildcard imports carry no simple name.
	if isStatic || isWildcard {
		return
	}
	simple := importPath
	if i := strings.LastIndex(importPath, "."); i >= 0 {
		simple = importPath[i+1:]
	}
	if simple != "" {
		w.importMap[simple] = importPath
	}
}

func (w *astWalker) handleClassLike(node *sitter.Node, kind string) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)

	modifiers := findChildByKind(node, "modifiers")
	modifierText := ""
	var annotations []javaAnnotation
	if modifiers != nil {
		modifierText = nodeText(modifiers, w.src)
		annotations = parseAnnotations(modifiers, w.src)
	}
	// A top-level type is exported when public; nested types inherit visibility
	// loosely — treat anything not explicitly private as part of the surface.
	exported := strings.Contains(modifierText, "public") ||
		(!strings.Contains(modifierText, "private") && len(w.typeStack) > 0)

	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.canonicalName(w.qualify(name)),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": kind,
			"exported":    exported,
			"language":    "java",
			"fqn":         w.fqn(w.qualify(name)),
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: w.dir},
		},
	}
	if strings.Contains(modifierText, "abstract") {
		f.Props["abstract"] = true
	}
	if node.Kind() == "record_declaration" {
		f.Props["record"] = true
	}
	if node.Kind() == "annotation_type_declaration" {
		f.Props["annotation_class"] = true
	}

	// Inheritance: `extends` superclass + `implements`/`extends` interfaces.
	for _, st := range w.supertypeTargets(node) {
		f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelImplements, Target: st})
	}

	// Framework classification (Spring component / JPA / Dubbo SPI) mutates props
	// and may emit a companion storage fact.
	classifyComponent(&f, name, annotations, w.supertypeSimpleNames(node))
	if sf := detectJpaStorage(name, annotations, w.relFile, int(node.StartPosition().Row)+1, w.dir); sf != nil {
		w.out = append(w.out, *sf)
	}

	w.out = append(w.out, f)
	owner := &w.out[len(w.out)-1]
	w.pushOwner(owner)

	// Enter the type scope.
	body := classBody(node)
	w.typeStack = append(w.typeStack, name)
	w.methodStack = append(w.methodStack, collectMethodNames(body, w.src))
	w.routeStack = append(w.routeStack, routeScope{
		isController: isSpringController(annotations),
		basePath:     requestMappingPath(annotations),
	})

	// Constructor-based DI: a class with a single constructor, or one annotated
	// @Autowired/@Inject, injects each of that constructor's parameter types.
	w.handleConstructorInjection(node, body, owner, annotations)
	// Field-level @Autowired/@Inject and Lombok @RequiredArgsConstructor over
	// `private final` fields also produce injection edges; emitted while walking
	// the body below.

	if body != nil {
		w.walkBody(body, owner)
	}

	w.routeStack = w.routeStack[:len(w.routeStack)-1]
	w.typeStack = w.typeStack[:len(w.typeStack)-1]
	w.methodStack = w.methodStack[:len(w.methodStack)-1]
	w.popOwner()
}

// walkBody iterates the direct members of a class/interface/enum body, handling
// nested declarations, methods, and fields. Non-declaration nodes are scanned for
// constructor calls attributed to `owner`.
func (w *astWalker) walkBody(body *sitter.Node, owner *facts.Fact) {
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		c := body.Child(i)
		switch c.Kind() {
		case "class_declaration":
			w.handleClassLike(c, facts.SymbolClass)
		case "interface_declaration":
			w.handleClassLike(c, facts.SymbolInterface)
		case "enum_declaration":
			w.handleClassLike(c, facts.SymbolEnum)
		case "record_declaration":
			w.handleClassLike(c, facts.SymbolClass)
		case "annotation_type_declaration":
			w.handleClassLike(c, facts.SymbolInterface)
		case "method_declaration", "constructor_declaration":
			w.handleMethod(c)
		case "field_declaration":
			w.handleField(c, owner)
		default:
			// init blocks, enum constants, etc. — scan for constructor calls.
			w.walkForCalls(c)
		}
	}
}

func (w *astWalker) handleMethod(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)

	modifiers := findChildByKind(node, "modifiers")
	modifierText := ""
	var annotations []javaAnnotation
	if modifiers != nil {
		modifierText = nodeText(modifiers, w.src)
		annotations = parseAnnotations(modifiers, w.src)
	}
	exported := strings.Contains(modifierText, "public")

	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.canonicalName(w.qualify(name)),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolMethod,
			"exported":    exported,
			"language":    "java",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: w.dir},
		},
	}
	if t := w.enclosingType(); t != "" {
		f.Props["receiver"] = t
	}
	if strings.Contains(modifierText, "static") {
		f.Props["static"] = true
	}

	// Spring route: a request-mapping annotation on a controller method.
	if rs := w.currentRoute(); rs != nil && rs.isController {
		for _, rf := range springRouteFacts(rs.basePath, annotations, w.relFile,
			int(node.StartPosition().Row)+1, w.dir, w.canonicalName(w.qualify(name))) {
			w.out = append(w.out, rf)
		}
	}

	w.out = append(w.out, f)
	owner := &w.out[len(w.out)-1]
	w.pushOwner(owner)
	if body := node.ChildByFieldName("body"); body != nil {
		w.walkForCalls(body)
	}
	w.popOwner()
}

func (w *astWalker) handleField(node *sitter.Node, owner *facts.Fact) {
	modifiers := findChildByKind(node, "modifiers")
	modifierText := ""
	var annotations []javaAnnotation
	if modifiers != nil {
		modifierText = nodeText(modifiers, w.src)
		annotations = parseAnnotations(modifiers, w.src)
	}
	exported := strings.Contains(modifierText, "public")

	symbolKind := facts.SymbolVariable
	if strings.Contains(modifierText, "final") {
		symbolKind = facts.SymbolConstant
	}

	// Field type — used for DI edges when @Autowired/@Inject is present.
	typeNode := node.ChildByFieldName("type")
	typeTarget := w.targetForType(typeNode)
	injected := hasAnnotation(annotations, "Autowired", "Inject", "Resource", "Reference")
	// A `static final String FOO = "literal"` constant exposes its value so that
	// references to it (e.g. @Table(name = FOO)) can be resolved in a later pass.
	captureValue := strings.Contains(modifierText, "static") &&
		strings.Contains(modifierText, "final") &&
		typeFullName(typeNode, w.src) == "String"

	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Kind() != "variable_declarator" {
			continue
		}
		nameNode := c.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nodeText(nameNode, w.src)
		props := map[string]any{
			"symbol_kind": symbolKind,
			"exported":    exported,
			"language":    "java",
		}
		if captureValue && isScreamingSnake(name) {
			if val, ok := stringLiteralValue(c.ChildByFieldName("value"), w.src); ok {
				props["value"] = val
			}
		}
		w.out = append(w.out, facts.Fact{
			Kind:  facts.KindSymbol,
			Name:  w.canonicalName(w.qualify(name)),
			File:  w.relFile,
			Line:  int(c.StartPosition().Row) + 1,
			Props: props,
			Relations: []facts.Relation{
				{Kind: facts.RelDeclares, Target: w.dir},
			},
		})
	}

	if injected && typeTarget != "" && owner != nil {
		owner.Relations = append(owner.Relations, facts.Relation{Kind: facts.RelInjects, Target: typeTarget})
	}
	// A field initializer may contain a constructor call (`= new Foo()`).
	w.walkForCalls(node)
}

// handleConstructorInjection emits RelInjects edges from `owner` to each parameter
// type of an injectable constructor: a sole constructor, a constructor annotated
// @Autowired/@Inject, or (Lombok) when the class carries @RequiredArgsConstructor /
// @AllArgsConstructor.
func (w *astWalker) handleConstructorInjection(decl, body *sitter.Node, owner *facts.Fact, classAnns []javaAnnotation) {
	lombokInject := hasAnnotation(classAnns, "RequiredArgsConstructor", "AllArgsConstructor")

	// record_declaration parameters are constructor parameters too.
	if decl.Kind() == "record_declaration" {
		if params := decl.ChildByFieldName("parameters"); params != nil && lombokInject {
			w.injectParams(params, owner)
		}
	}

	if lombokInject {
		// Inject each `private final` field's type.
		if body != nil {
			for i := uint(0); i < uint(body.ChildCount()); i++ {
				c := body.Child(i)
				if c.Kind() != "field_declaration" {
					continue
				}
				mods := findChildByKind(c, "modifiers")
				if mods == nil || !strings.Contains(nodeText(mods, w.src), "final") {
					continue
				}
				if t := w.targetForType(c.ChildByFieldName("type")); t != "" && owner != nil {
					owner.Relations = append(owner.Relations, facts.Relation{Kind: facts.RelInjects, Target: t})
				}
			}
		}
	}

	if body == nil {
		return
	}
	var ctors []*sitter.Node
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		if c := body.Child(i); c.Kind() == "constructor_declaration" {
			ctors = append(ctors, c)
		}
	}
	for _, ctor := range ctors {
		mods := findChildByKind(ctor, "modifiers")
		annotated := false
		if mods != nil {
			annotated = hasAnnotation(parseAnnotations(mods, w.src), "Autowired", "Inject")
		}
		if annotated || (len(ctors) == 1 && !lombokInject) {
			if params := ctor.ChildByFieldName("parameters"); params != nil {
				w.injectParams(params, owner)
			}
		}
	}
}

func (w *astWalker) injectParams(params *sitter.Node, owner *facts.Fact) {
	if owner == nil {
		return
	}
	for i := uint(0); i < uint(params.ChildCount()); i++ {
		p := params.Child(i)
		if p.Kind() != "formal_parameter" {
			continue
		}
		if t := w.targetForType(p.ChildByFieldName("type")); t != "" {
			owner.Relations = append(owner.Relations, facts.Relation{Kind: facts.RelInjects, Target: t})
		}
	}
}

// walkForCalls recursively scans a subtree for object_creation_expression (→
// RelInstantiates) and method_invocation (→ RelCalls for resolvable same-class
// calls), attributing each to the current owner. Nested type declarations are
// dispatched to their own handlers so their calls are attributed correctly.
func (w *astWalker) walkForCalls(node *sitter.Node) {
	if node == nil {
		return
	}
	switch node.Kind() {
	case "class_declaration":
		w.handleClassLike(node, facts.SymbolClass)
		return
	case "interface_declaration":
		w.handleClassLike(node, facts.SymbolInterface)
		return
	case "enum_declaration":
		w.handleClassLike(node, facts.SymbolEnum)
		return
	case "record_declaration":
		w.handleClassLike(node, facts.SymbolClass)
		return
	case "object_creation_expression":
		if t := w.targetForType(node.ChildByFieldName("type")); t != "" {
			if owner := w.currentOwner(); owner != nil {
				owner.Relations = append(owner.Relations, facts.Relation{Kind: facts.RelInstantiates, Target: t})
			}
		}
	case "method_invocation":
		w.handleInvocation(node)
	}

	for i := uint(0); i < uint(node.ChildCount()); i++ {
		w.walkForCalls(node.Child(i))
	}
}

func (w *astWalker) handleInvocation(node *sitter.Node) {
	owner := w.currentOwner()
	if owner == nil {
		return
	}
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)
	obj := node.ChildByFieldName("object")

	// Resolve bare `foo()` and `this.foo()` calls against the enclosing class's
	// own methods. Calls on other receivers are left unresolved (the receiver's
	// type is not tracked), matching the Kotlin extractor's conservative model.
	isThis := obj != nil && nodeText(obj, w.src) == "this"
	if obj == nil || isThis {
		if methods := w.currentMethods(); methods[name] {
			owner.Relations = append(owner.Relations, facts.Relation{
				Kind:   facts.RelCalls,
				Target: w.dir + "." + w.enclosingType() + "." + name,
			})
		}
	}
}

// targetForType returns a relation target for a `_type` node: the rightmost simple
// name resolved through the import map to an FQN, a same-package FQN when not
// imported, or the written FQN when the reference is already qualified. Returns ""
// for primitive/void/unresolvable types.
func (w *astWalker) targetForType(typeNode *sitter.Node) string {
	if typeNode == nil {
		return ""
	}
	full := typeFullName(typeNode, w.src)
	if full == "" {
		return ""
	}
	if isPrimitiveType(full) {
		return ""
	}
	simple := full
	if i := strings.LastIndex(full, "."); i >= 0 {
		// Already qualified in source — use as written.
		return full
	}
	if fqn, ok := w.importMap[simple]; ok {
		return fqn
	}
	if javaLangTypes[simple] {
		return ""
	}
	if w.pkg != "" {
		return w.pkg + "." + simple
	}
	return simple
}

// supertypeTargets returns canonicalization targets (FQNs) for a type's superclass
// and implemented/extended interfaces.
func (w *astWalker) supertypeTargets(node *sitter.Node) []string {
	var out []string
	for _, n := range w.supertypeNodes(node) {
		if t := w.targetForType(n); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// supertypeSimpleNames returns the simple names of a type's supertypes (used by
// component classification, e.g. detecting Spring Data repository interfaces).
func (w *astWalker) supertypeSimpleNames(node *sitter.Node) []string {
	var out []string
	for _, n := range w.supertypeNodes(node) {
		if s := lastTypeComponent(typeFullName(n, w.src)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (w *astWalker) supertypeNodes(node *sitter.Node) []*sitter.Node {
	var out []*sitter.Node
	if sc := node.ChildByFieldName("superclass"); sc != nil {
		out = append(out, firstTypeChild(sc))
	}
	// `interfaces` field (class/enum/record) or `extends_interfaces` child (interface).
	if iface := node.ChildByFieldName("interfaces"); iface != nil {
		out = append(out, typeListChildren(iface)...)
	}
	if ext := findChildByKind(node, "extends_interfaces"); ext != nil {
		out = append(out, typeListChildren(ext)...)
	}
	// Filter nils.
	kept := out[:0]
	for _, n := range out {
		if n != nil {
			kept = append(kept, n)
		}
	}
	return kept
}

// --- tree-sitter / type helpers ---

func classBody(node *sitter.Node) *sitter.Node {
	if b := node.ChildByFieldName("body"); b != nil {
		return b
	}
	return nil
}

// typeListChildren returns the concrete `_type` children of a super_interfaces /
// extends_interfaces node, which wrap a single type_list.
func typeListChildren(node *sitter.Node) []*sitter.Node {
	tl := findChildByKind(node, "type_list")
	if tl == nil {
		return nil
	}
	var out []*sitter.Node
	for i := uint(0); i < uint(tl.ChildCount()); i++ {
		c := tl.Child(i)
		if c.IsNamed() && c.Kind() != "annotation" && c.Kind() != "marker_annotation" {
			out = append(out, c)
		}
	}
	return out
}

// firstTypeChild returns the first named, non-annotation child of a wrapper node
// (e.g. the `_type` under a superclass node).
func firstTypeChild(node *sitter.Node) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.IsNamed() && c.Kind() != "annotation" && c.Kind() != "marker_annotation" {
			return c
		}
	}
	return nil
}

// typeFullName returns the source-written dotted name of a `_type` node, stripping
// generic arguments and array dimensions. For `java.util.List<String>` it returns
// "java.util.List"; for `Map<K,V>` it returns "Map".
func typeFullName(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "type_identifier", "scoped_type_identifier":
		return nodeText(node, src)
	case "generic_type":
		// First named child is the base type (type_identifier or scoped_type_identifier).
		if base := firstNamedChild(node); base != nil {
			return typeFullName(base, src)
		}
	case "annotated_type":
		// The type follows the annotations.
		for i := uint(0); i < uint(node.ChildCount()); i++ {
			c := node.Child(i)
			if c.IsNamed() && c.Kind() != "annotation" && c.Kind() != "marker_annotation" {
				return typeFullName(c, src)
			}
		}
	case "array_type":
		if el := node.ChildByFieldName("element"); el != nil {
			return typeFullName(el, src)
		}
	}
	// Primitive / void / fallback: take the raw text, stripped of generics/arrays.
	t := nodeText(node, src)
	if i := strings.IndexAny(t, "<[ "); i >= 0 {
		t = t[:i]
	}
	return strings.TrimSpace(t)
}

func lastTypeComponent(full string) string {
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}

func collectMethodNames(body *sitter.Node, src []byte) map[string]bool {
	methods := make(map[string]bool)
	if body == nil {
		return methods
	}
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		c := body.Child(i)
		if c.Kind() != "method_declaration" {
			continue
		}
		if nameNode := c.ChildByFieldName("name"); nameNode != nil {
			methods[nodeText(nameNode, src)] = true
		}
	}
	return methods
}

// isScreamingSnake reports whether a name is an UPPER_SNAKE_CASE constant
// identifier (the convention for table/column name constants).
func isScreamingSnake(s string) bool {
	if s == "" {
		return false
	}
	hasLetter := false
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return hasLetter
}

// stringLiteralValue returns the unquoted contents of a string_literal node.
func stringLiteralValue(node *sitter.Node, src []byte) (string, bool) {
	if node == nil || node.Kind() != "string_literal" {
		return "", false
	}
	return strings.Trim(nodeText(node, src), `"`), true
}

func isPrimitiveType(name string) bool {
	switch name {
	case "void", "boolean", "byte", "short", "int", "long", "char", "float", "double", "var":
		return true
	}
	return false
}

// javaLangTypes are implicitly imported java.lang types that appear as bare names
// without an import statement; resolving them would create dangling edges.
var javaLangTypes = map[string]bool{
	"Object": true, "String": true, "Integer": true, "Long": true, "Short": true,
	"Byte": true, "Character": true, "Boolean": true, "Float": true, "Double": true,
	"Number": true, "Math": true, "System": true, "Thread": true, "Runnable": true,
	"Exception": true, "RuntimeException": true, "Throwable": true, "Error": true,
	"IllegalArgumentException": true, "IllegalStateException": true,
	"NullPointerException": true, "UnsupportedOperationException": true,
	"Class": true, "Enum": true, "Iterable": true, "Comparable": true, "CharSequence": true,
	"StringBuilder": true, "StringBuffer": true, "Void": true, "Override": true,
	"Deprecated": true, "SuppressWarnings": true, "FunctionalInterface": true,
	"AutoCloseable": true, "Cloneable": true, "ClassLoader": true, "Process": true,
}

func isCapitalized(s string) bool {
	if s == "" {
		return false
	}
	return unicode.IsUpper([]rune(s)[0])
}

func findChildByKind(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Kind() == kind {
			return c
		}
	}
	return nil
}

func firstNamedChild(node *sitter.Node) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.IsNamed() {
			return c
		}
	}
	return nil
}

func nodeText(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	return string(src[node.StartByte():node.EndByte()])
}
