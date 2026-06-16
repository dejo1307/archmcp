package cppextractor

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/enola-labs/enola/internal/facts"
	cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractFileAST parses a C++ translation unit with tree-sitter and emits
// architectural facts (parity with the Swift/Kotlin extractors).
//
// Symbols are named "<dir>.<ns1::ns2::...::Class::member>": enola's "<dir>."
// module convention on the outside, native C++ "::" scope separators inside. The
// scheme is what makes an out-of-line definition "Foo::bar" (parsed from a
// qualified_identifier) produce a byte-identical name to its in-class declaration,
// so the header/source dedup pass in Extract collapses them automatically.
//
// Call-graph / inheritance / instantiation edge targets are emitted as bare names
// here (e.g. "Msg::Error", "GEntity") and canonicalised to "<dir>.<...>" by the
// post-pass in Extract, which holds the project-wide type index.
func extractFileAST(src []byte, relFile string) []facts.Fact {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(cpp.Language())); err != nil {
		return nil
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil
	}
	defer tree.Close()

	w := &astWalker{
		src:         src,
		relFile:     relFile,
		dir:         filepath.Dir(relFile),
		fileMethods: buildFileMethodIndex(tree.RootNode(), src),
	}
	w.walkDeclList(tree.RootNode())
	return w.out
}

// buildFileMethodIndex collects, per simple class name, the set of method names
// declared or defined anywhere in this file (inline prototypes/definitions and
// out-of-line "Class::method" definitions). It lets an out-of-line method body
// resolve calls to sibling methods of the same class to their canonical names.
func buildFileMethodIndex(root *sitter.Node, src []byte) map[string]map[string]bool {
	idx := make(map[string]map[string]bool)
	add := func(owner, method string) {
		if owner == "" || method == "" {
			return
		}
		if idx[owner] == nil {
			idx[owner] = make(map[string]bool)
		}
		idx[owner][method] = true
	}
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Kind() {
		case "class_specifier", "struct_specifier", "union_specifier":
			if nn := n.ChildByFieldName("name"); nn != nil {
				owner := simpleTypeName(nn, src)
				for m := range collectMethodNames(n.ChildByFieldName("body"), src) {
					add(owner, m)
				}
			}
		case "function_definition":
			if fd := findFunctionDeclarator(n.ChildByFieldName("declarator")); fd != nil {
				if nameNode := fd.ChildByFieldName("declarator"); nameNode != nil && nameNode.Kind() == "qualified_identifier" {
					scopes, leaf := splitQualified(nameNode, src)
					if len(scopes) > 0 {
						add(scopes[len(scopes)-1], leaf)
					}
				}
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			visit(n.Child(i))
		}
	}
	visit(root)
	return idx
}

type astWalker struct {
	src     []byte
	relFile string
	dir     string

	out []facts.Fact

	// nsStack holds the enclosing namespace components (e.g. ["gmsh","fs"]).
	nsStack []string
	// typeStack holds the enclosing class/struct/union names. methodStack is
	// parallel and holds the set of method names declared directly in each
	// enclosing type, used to resolve same-type bare/this calls.
	typeStack   []string
	methodStack []map[string]bool

	// ownerStack holds INDICES into out (not pointers) of the symbol fact whose
	// body is currently being walked. Indices stay valid across append/realloc,
	// which pointers would not. -1 means no owner.
	ownerStack []int

	// fileMethods maps a simple class name to the set of its method names declared
	// anywhere in this file, so out-of-line method bodies can resolve sibling calls.
	fileMethods map[string]map[string]bool
}

func (w *astWalker) pushType(name string, methods map[string]bool) {
	w.typeStack = append(w.typeStack, name)
	w.methodStack = append(w.methodStack, methods)
}

func (w *astWalker) popType() {
	w.typeStack = w.typeStack[:len(w.typeStack)-1]
	w.methodStack = w.methodStack[:len(w.methodStack)-1]
}

func (w *astWalker) currentMethods() map[string]bool {
	if len(w.methodStack) == 0 {
		return nil
	}
	return w.methodStack[len(w.methodStack)-1]
}

// scopePrefix returns the "ns::...::Class::" prefix for the current position, or
// "" at file scope.
func (w *astWalker) scopePrefix() string {
	n := len(w.nsStack) + len(w.typeStack)
	if n == 0 {
		return ""
	}
	parts := make([]string, 0, n)
	parts = append(parts, w.nsStack...)
	parts = append(parts, w.typeStack...)
	return strings.Join(parts, "::") + "::"
}

func (w *astWalker) qualify(name string) string { return w.scopePrefix() + name }

// factName returns the canonical "<dir>.<scope>::<name>" fact name.
func (w *astWalker) factName(name string) string { return w.dir + "." + w.qualify(name) }

// enclosingTypeReceiver returns the innermost enclosing class name (the receiver
// for a method), or "" when at namespace/file scope.
func (w *astWalker) enclosingTypeReceiver() string {
	if len(w.typeStack) == 0 {
		return ""
	}
	return w.typeStack[len(w.typeStack)-1]
}

func (w *astWalker) pushOwner(i int) { w.ownerStack = append(w.ownerStack, i) }
func (w *astWalker) popOwner()       { w.ownerStack = w.ownerStack[:len(w.ownerStack)-1] }
func (w *astWalker) currentOwner() int {
	if len(w.ownerStack) == 0 {
		return -1
	}
	return w.ownerStack[len(w.ownerStack)-1]
}

// addOwnerEdge appends a relation to the current owner fact, if any, avoiding
// duplicate relations on the same fact.
func (w *astWalker) addOwnerEdge(kind, target string) {
	idx := w.currentOwner()
	if idx < 0 || target == "" {
		return
	}
	for _, r := range w.out[idx].Relations {
		if r.Kind == kind && r.Target == target {
			return
		}
	}
	w.out[idx].Relations = append(w.out[idx].Relations, facts.Relation{Kind: kind, Target: target})
}

// walkDeclList dispatches every child of a translation_unit / declaration_list /
// preproc guard body to walkDecl.
func (w *astWalker) walkDeclList(node *sitter.Node) {
	if node == nil {
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		w.walkDecl(node.Child(i))
	}
}

// walkDecl handles a single top-level / namespace-level / preproc-guarded
// declaration node.
func (w *astWalker) walkDecl(node *sitter.Node) {
	if node == nil {
		return
	}
	switch node.Kind() {
	case "preproc_include":
		w.handleInclude(node)
	case "namespace_definition":
		w.handleNamespace(node)
	case "class_specifier":
		w.handleClassLike(node, facts.SymbolClass)
	case "struct_specifier":
		w.handleClassLike(node, facts.SymbolStruct)
	case "union_specifier":
		w.handleClassLike(node, facts.SymbolStruct)
	case "enum_specifier":
		w.handleEnum(node)
	case "function_definition":
		w.handleFunctionDefinition(node)
	case "template_declaration":
		w.handleTemplate(node)
	case "declaration":
		w.handleDeclaration(node)
	case "type_definition":
		w.handleTypedef(node)
	case "alias_declaration":
		w.handleUsing(node)
	case "linkage_specification":
		// extern "C" { ... } — recurse into the wrapped body.
		w.walkDeclList(findChildByKind(node, "declaration_list"))
	case "preproc_if", "preproc_ifdef", "preproc_else", "preproc_elif",
		"preproc_elifdef":
		// Descend through #if/#ifdef guards: declarations inside them are direct
		// children (after the condition/name field). gmsh wraps large amounts of
		// code in #if defined(HAVE_*); headers wrap everything in #ifndef guards.
		w.walkGuardChildren(node)
	}
}

// walkGuardChildren walks the declaration children of a preproc guard, skipping
// the field children (the condition/name and the nested alternative branch, which
// is itself a preproc_* node handled via walkDecl).
func (w *astWalker) walkGuardChildren(node *sitter.Node) {
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		switch fieldName := node.FieldNameForChild(uint32(i)); fieldName {
		case "condition", "name":
			continue
		}
		w.walkDecl(child)
	}
}

func (w *astWalker) handleNamespace(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	var pushed int
	if nameNode != nil {
		// namespace_identifier (simple) or nested_namespace_specifier (C++17 A::B).
		for _, comp := range strings.Split(nodeText(nameNode, w.src), "::") {
			comp = strings.TrimSpace(comp)
			if comp == "" {
				continue
			}
			w.nsStack = append(w.nsStack, comp)
			pushed++
		}
	}
	// nameNode == nil ⇒ anonymous namespace: push nothing.
	w.walkDeclList(node.ChildByFieldName("body"))
	w.nsStack = w.nsStack[:len(w.nsStack)-pushed]
}

func (w *astWalker) handleClassLike(node *sitter.Node, kind string) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return // anonymous struct/union (e.g. inside a typedef) — skip
	}
	name := simpleTypeName(nameNode, w.src)
	if name == "" {
		return
	}
	body := node.ChildByFieldName("body")
	if body == nil {
		return // forward declaration (class Foo;) — emit nothing
	}

	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.factName(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": kind,
			"exported":    true, // C++ has no file-private types; visibility is via access specifiers
			"language":    "cpp",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	}
	if node.Kind() == "union_specifier" {
		f.Props["union"] = true
	}
	for _, base := range baseClassNames(node, w.src) {
		f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelImplements, Target: base})
	}

	w.out = append(w.out, f)
	ownerIdx := len(w.out) - 1

	w.pushOwner(ownerIdx)
	w.pushType(name, collectMethodNames(body, w.src))
	w.walkClassBody(body)
	w.popType()
	w.popOwner()
}

// walkClassBody iterates the direct members of a field_declaration_list.
func (w *astWalker) walkClassBody(body *sitter.Node) {
	for i := uint(0); i < body.ChildCount(); i++ {
		c := body.Child(i)
		switch c.Kind() {
		case "field_declaration":
			w.handleFieldDeclaration(c)
		case "function_definition":
			w.handleFunctionDefinition(c) // inline method definition
		case "class_specifier":
			w.handleClassLike(c, facts.SymbolClass)
		case "struct_specifier":
			w.handleClassLike(c, facts.SymbolStruct)
		case "union_specifier":
			w.handleClassLike(c, facts.SymbolStruct)
		case "enum_specifier":
			w.handleEnum(c)
		case "template_declaration":
			w.handleTemplate(c)
		case "type_definition":
			w.handleTypedef(c)
		case "alias_declaration":
			w.handleUsing(c)
		case "friend_declaration":
			// friend functions/classes: walk the inner decl so e.g. friend
			// operator definitions still contribute symbols, but don't crash.
			for j := uint(0); j < c.ChildCount(); j++ {
				w.walkDecl(c.Child(j))
			}
		}
	}
}

func (w *astWalker) handleEnum(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return // anonymous enum — skip
	}
	name := simpleTypeName(nameNode, w.src)
	if name == "" {
		return
	}
	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.factName(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolEnum,
			"exported":    true,
			"language":    "cpp",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	}
	if isScopedEnum(node, w.src) {
		f.Props["scoped"] = true
	}
	w.out = append(w.out, f)
}

// handleFunctionDefinition handles a function_definition, which may be a free
// function, an inline method (inside a class body), or an OUT-OF-LINE method /
// namespaced-function definition (declarator is a qualified_identifier).
func (w *astWalker) handleFunctionDefinition(node *sitter.Node) {
	fdecl := findFunctionDeclarator(node.ChildByFieldName("declarator"))
	if fdecl == nil {
		return
	}
	nameNode := fdecl.ChildByFieldName("declarator")
	if nameNode == nil {
		return
	}

	var symbolName, receiver string
	var outOfLineScopes []string // scope chain to push for an out-of-line def
	switch nameNode.Kind() {
	case "qualified_identifier":
		// Out-of-line definition: "A::B::method". Combine any enclosing namespace
		// with the qualified scope so the name matches the in-class declaration.
		scopes, leaf := splitQualified(nameNode, w.src)
		if leaf == "" {
			return
		}
		comps := make([]string, 0, len(w.nsStack)+len(scopes)+1)
		comps = append(comps, w.nsStack...)
		comps = append(comps, scopes...)
		comps = append(comps, leaf)
		symbolName = w.dir + "." + strings.Join(comps, "::")
		if len(scopes) > 0 {
			receiver = scopes[len(scopes)-1] // immediate scope owning the definition
			outOfLineScopes = scopes
		}
	default:
		leaf := declaratorLeafName(nameNode, w.src)
		if leaf == "" {
			return
		}
		symbolName = w.factName(leaf)
		receiver = w.enclosingTypeReceiver()
	}

	sk := facts.SymbolFunc
	if receiver != "" {
		sk = facts.SymbolMethod
	}
	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: symbolName,
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": sk,
			"exported":    true,
			"language":    "cpp",
			"has_body":    true,
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	}
	if receiver != "" {
		f.Props["receiver"] = receiver
	}
	w.applyFuncQualifiers(&f, node, fdecl)

	w.out = append(w.out, f)
	idx := len(w.out) - 1

	w.pushOwner(idx)
	// For an out-of-line def, push the full scope chain so the body's qualified
	// context (and thus sibling/this call resolution) matches the in-class names.
	// Only the innermost scope (the class) carries the file-wide method set.
	for i, comp := range outOfLineScopes {
		var methods map[string]bool
		if i == len(outOfLineScopes)-1 {
			methods = w.fileMethods[comp]
		}
		w.pushType(comp, methods)
	}
	if body := node.ChildByFieldName("body"); body != nil {
		w.walkForCalls(body)
	}
	for range outOfLineScopes {
		w.popType()
	}
	w.popOwner()
}

// handleFieldDeclaration handles a member of a class body: either a member
// function declaration (prototype) or one-or-more data members.
func (w *astWalker) handleFieldDeclaration(node *sitter.Node) {
	if fdecl := findFunctionDeclarator(node.ChildByFieldName("declarator")); fdecl != nil {
		// Member function prototype (no body) — e.g. "virtual void clear();".
		nameNode := fdecl.ChildByFieldName("declarator")
		leaf := declaratorLeafName(nameNode, w.src)
		if leaf == "" {
			return
		}
		f := facts.Fact{
			Kind: facts.KindSymbol,
			Name: w.factName(leaf),
			File: w.relFile,
			Line: int(node.StartPosition().Row) + 1,
			Props: map[string]any{
				"symbol_kind": facts.SymbolMethod,
				"exported":    true,
				"language":    "cpp",
				"receiver":    w.enclosingTypeReceiver(),
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
		}
		w.applyFuncQualifiers(&f, node, fdecl)
		w.out = append(w.out, f)
		return
	}

	// Data member(s): "Foo *a, b;" → one SymbolVariable per declared name.
	constMember := strings.Contains(declTypePrefix(node, w.src), "const")
	for i := uint(0); i < node.ChildCount(); i++ {
		if node.FieldNameForChild(uint32(i)) != "declarator" {
			continue
		}
		leaf := declaratorLeafName(node.Child(i), w.src)
		if leaf == "" {
			continue
		}
		kind := facts.SymbolVariable
		if constMember {
			kind = facts.SymbolConstant
		}
		w.out = append(w.out, facts.Fact{
			Kind: facts.KindSymbol,
			Name: w.factName(leaf),
			File: w.relFile,
			Line: int(node.StartPosition().Row) + 1,
			Props: map[string]any{
				"symbol_kind": kind,
				"exported":    true,
				"language":    "cpp",
				"receiver":    w.enclosingTypeReceiver(),
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
		})
	}
}

// handleTemplate unwraps a template_declaration to its inner class/function/etc.
// declaration and marks every fact the inner decl produced as templated.
func (w *astWalker) handleTemplate(node *sitter.Node) {
	var inner *sitter.Node
	for i := uint(0); i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c.Kind() == "template_parameter_list" || !c.IsNamed() {
			continue
		}
		inner = c
	}
	if inner == nil {
		return
	}
	before := len(w.out)
	w.walkDecl(inner)
	for i := before; i < len(w.out); i++ {
		if w.out[i].Props != nil {
			w.out[i].Props["templated"] = true
		}
	}
}

// handleDeclaration handles a translation-unit / namespace-level declaration:
// free-function prototypes (which dedup with their out-of-line definitions) and
// class/struct/enum specifiers used as a declaration's type.
func (w *astWalker) handleDeclaration(node *sitter.Node) {
	// `class Foo {...} bar;` / `struct X {...};` — the specifier is the type field.
	if t := node.ChildByFieldName("type"); t != nil {
		switch t.Kind() {
		case "class_specifier":
			w.handleClassLike(t, facts.SymbolClass)
		case "struct_specifier":
			w.handleClassLike(t, facts.SymbolStruct)
		case "union_specifier":
			w.handleClassLike(t, facts.SymbolStruct)
		case "enum_specifier":
			w.handleEnum(t)
		}
	}
	// Free-function prototype: declarator is (or wraps) a function_declarator whose
	// own declarator is a plain identifier or a qualified_identifier.
	fdecl := findFunctionDeclarator(node.ChildByFieldName("declarator"))
	if fdecl == nil {
		return
	}
	nameNode := fdecl.ChildByFieldName("declarator")
	if nameNode == nil {
		return
	}
	var symbolName, receiver string
	switch nameNode.Kind() {
	case "qualified_identifier":
		scopes, leaf := splitQualified(nameNode, w.src)
		if leaf == "" {
			return
		}
		comps := append(append([]string{}, w.nsStack...), scopes...)
		comps = append(comps, leaf)
		symbolName = w.dir + "." + strings.Join(comps, "::")
		if n := len(comps) - 1; n > 0 {
			receiver = comps[n-1]
		}
	default:
		leaf := declaratorLeafName(nameNode, w.src)
		if leaf == "" {
			return
		}
		symbolName = w.factName(leaf)
		receiver = w.enclosingTypeReceiver()
	}
	sk := facts.SymbolFunc
	if receiver != "" {
		sk = facts.SymbolMethod
	}
	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: symbolName,
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": sk,
			"exported":    true,
			"language":    "cpp",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	}
	if receiver != "" {
		f.Props["receiver"] = receiver
	}
	w.applyFuncQualifiers(&f, node, fdecl)
	w.out = append(w.out, f)
}

func (w *astWalker) handleTypedef(node *sitter.Node) {
	leaf := declaratorLeafName(node.ChildByFieldName("declarator"), w.src)
	if leaf == "" {
		return
	}
	w.emitTypeAlias(leaf, node)
}

func (w *astWalker) handleUsing(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := simpleTypeName(nameNode, w.src)
	if name == "" {
		return
	}
	w.emitTypeAlias(name, node)
}

func (w *astWalker) emitTypeAlias(name string, node *sitter.Node) {
	w.out = append(w.out, facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.factName(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolType,
			"exported":    true,
			"language":    "cpp",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})
}

func (w *astWalker) handleInclude(node *sitter.Node) {
	pathNode := node.ChildByFieldName("path")
	if pathNode == nil {
		return
	}
	if pathNode.Kind() != "string_literal" {
		// system_lib_string (<vector>) and other forms are external — skip.
		return
	}
	inc := strings.Trim(nodeText(pathNode, w.src), `"`)
	if inc == "" {
		return
	}
	w.out = append(w.out, facts.Fact{
		Kind: facts.KindDependency,
		Name: w.dir + " -> " + inc,
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"language": "cpp",
			"include":  inc,
			"source":   "internal",
		},
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: inc}},
	})
}

// applyFuncQualifiers tags a function/method fact with static/virtual/const flags
// read from its signature text.
func (w *astWalker) applyFuncQualifiers(f *facts.Fact, node, fdecl *sitter.Node) {
	header := w.signatureText(node)
	if strings.Contains(header, "static") {
		f.Props["static"] = true
	}
	if strings.Contains(header, "virtual") {
		f.Props["virtual"] = true
	}
	// Trailing const: a type_qualifier child after the parameter_list.
	if fdecl != nil {
		params := fdecl.ChildByFieldName("parameters")
		for i := uint(0); i < fdecl.ChildCount(); i++ {
			c := fdecl.Child(i)
			if c.Kind() == "type_qualifier" && params != nil && c.StartByte() >= params.EndByte() {
				if nodeText(c, w.src) == "const" {
					f.Props["const"] = true
				}
			}
		}
	}
}

// signatureText returns the source from the declaration start up to the body
// (or the whole node when there is no body).
func (w *astWalker) signatureText(node *sitter.Node) string {
	end := node.EndByte()
	if body := node.ChildByFieldName("body"); body != nil {
		end = body.StartByte()
	}
	return string(w.src[node.StartByte():end])
}

// --- call graph ---

// walkForCalls recursively scans a function body for call_expression nodes and
// attaches edges to the current owner (mirrors the Swift walker):
//   - Type(...)        -> RelInstantiates (constructor)
//   - bare lower call  -> RelCalls (resolved via resolveCall)
//   - Ns::fn / T::m    -> RelCalls to "scope::name" (canonicalised later)
//   - this->m()        -> RelCalls to the enclosing type's method
func (w *astWalker) walkForCalls(node *sitter.Node) {
	if node == nil {
		return
	}
	if node.Kind() == "call_expression" {
		callee := node.ChildByFieldName("function")
		name, kind, root := calleeInfo(callee, w.src)
		switch {
		case name == "":
			// unresolved (call through pointer, etc.)
		case kind == calleeQualified:
			if !systemNamespaces[root] {
				w.addOwnerEdge(facts.RelCalls, root+"::"+name)
			}
		case kind == calleePlain:
			if isTypeName(name) {
				if !cppBuiltinTypes[name] {
					w.addOwnerEdge(facts.RelInstantiates, name)
				}
			} else if target := w.resolveCall(name); target != "" {
				w.addOwnerEdge(facts.RelCalls, target)
			}
		case kind == calleeField && root == "this":
			if w.currentMethods()[name] {
				w.addOwnerEdge(facts.RelCalls, w.factName(name))
			}
		}
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		w.walkForCalls(node.Child(i))
	}
}

// resolveCall maps a bare (unqualified) call name to a canonical symbol fact name:
// a same-type method, a suppressed builtin, or a same-scope free function.
func (w *astWalker) resolveCall(name string) string {
	if w.currentMethods()[name] {
		return w.factName(name)
	}
	if cppBuiltins[name] {
		return ""
	}
	return w.dir + "." + name
}

type calleeKind int

const (
	calleeNone calleeKind = iota
	calleePlain
	calleeQualified
	calleeField
)

// calleeInfo inspects a call_expression's callee node and returns the called leaf
// name, its kind, and (for qualified/field calls) the relevant root: the immediate
// scope for "A::B::f" or the receiver identifier ("this" for this->m()).
func calleeInfo(callee *sitter.Node, src []byte) (name string, kind calleeKind, root string) {
	if callee == nil {
		return "", calleeNone, ""
	}
	switch callee.Kind() {
	case "identifier":
		return nodeText(callee, src), calleePlain, ""
	case "qualified_identifier":
		scopes, leaf := splitQualified(callee, src)
		if leaf == "" || len(scopes) == 0 {
			return "", calleeNone, ""
		}
		return leaf, calleeQualified, scopes[len(scopes)-1]
	case "field_expression":
		field := callee.ChildByFieldName("field")
		arg := callee.ChildByFieldName("argument")
		if field == nil {
			return "", calleeNone, ""
		}
		r := ""
		if arg != nil && arg.Kind() == "this" {
			r = "this"
		} else if arg != nil {
			r = nodeText(arg, src)
		}
		return identifierLeaf(field, src), calleeField, r
	case "template_function":
		// foo<T>(...) — the name is under the "name" field.
		if n := callee.ChildByFieldName("name"); n != nil {
			return identifierLeaf(n, src), calleePlain, ""
		}
	}
	return "", calleeNone, ""
}

// --- node helpers ---

// findFunctionDeclarator descends through pointer/reference/array/parenthesized
// declarator wrappers to the function_declarator, or returns nil.
func findFunctionDeclarator(node *sitter.Node) *sitter.Node {
	for node != nil {
		switch node.Kind() {
		case "function_declarator":
			return node
		case "pointer_declarator", "reference_declarator", "array_declarator",
			"parenthesized_declarator", "init_declarator":
			node = node.ChildByFieldName("declarator")
			if node == nil {
				return nil
			}
		default:
			return nil
		}
	}
	return nil
}

// declaratorLeafName descends a declarator subtree to its leaf name (identifier,
// field_identifier, qualified_identifier text, operator_name or destructor_name).
func declaratorLeafName(node *sitter.Node, src []byte) string {
	for node != nil {
		switch node.Kind() {
		case "identifier", "field_identifier", "type_identifier":
			return nodeText(node, src)
		case "operator_name", "destructor_name":
			return strings.Join(strings.Fields(nodeText(node, src)), " ")
		case "qualified_identifier":
			scopes, leaf := splitQualified(node, src)
			if leaf == "" {
				return ""
			}
			return strings.Join(append(scopes, leaf), "::")
		case "function_declarator", "pointer_declarator", "reference_declarator",
			"array_declarator", "parenthesized_declarator", "init_declarator":
			node = node.ChildByFieldName("declarator")
		default:
			node = firstNamedChild(node)
		}
	}
	return ""
}

// identifierLeaf returns the simple identifier text of a (possibly qualified or
// templated) name node.
func identifierLeaf(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "identifier", "field_identifier", "type_identifier":
		return nodeText(node, src)
	case "qualified_identifier":
		_, leaf := splitQualified(node, src)
		return leaf
	case "template_method", "template_function", "template_type":
		if n := node.ChildByFieldName("name"); n != nil {
			return identifierLeaf(n, src)
		}
	}
	if id := findFirstIdentifier(node, src); id != nil {
		return nodeText(id, src)
	}
	return ""
}

// splitQualified descends the "name" chain of a qualified_identifier, returning
// the ordered scope components and the final leaf name. For "A::B::method" it
// returns (["A","B"], "method").
func splitQualified(node *sitter.Node, src []byte) (scopes []string, leaf string) {
	cur := node
	for cur != nil && cur.Kind() == "qualified_identifier" {
		if s := cur.ChildByFieldName("scope"); s != nil {
			scopes = append(scopes, nodeText(s, src))
		}
		cur = cur.ChildByFieldName("name")
	}
	if cur == nil {
		return scopes, ""
	}
	switch cur.Kind() {
	case "operator_name", "destructor_name":
		return scopes, strings.Join(strings.Fields(nodeText(cur, src)), " ")
	case "template_type", "template_function", "template_method":
		if n := cur.ChildByFieldName("name"); n != nil {
			return scopes, nodeText(n, src)
		}
	}
	return scopes, nodeText(cur, src)
}

// baseClassNames returns the simple base-class names from a class/struct's
// base_class_clause (access specifiers and virtual keywords are anonymous tokens
// and are skipped; template arguments and namespaces are stripped).
func baseClassNames(node *sitter.Node, src []byte) []string {
	bc := findChildByKind(node, "base_class_clause")
	if bc == nil {
		return nil
	}
	var names []string
	for i := uint(0); i < bc.ChildCount(); i++ {
		c := bc.Child(i)
		switch c.Kind() {
		case "type_identifier", "qualified_identifier", "template_type", "qualified_type":
			if name := simpleTypeName(c, src); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// collectMethodNames returns the set of method names declared directly in a class
// body (from both inline definitions and prototypes), used to resolve same-type
// bare/this calls.
func collectMethodNames(body *sitter.Node, src []byte) map[string]bool {
	methods := make(map[string]bool)
	if body == nil {
		return methods
	}
	for i := uint(0); i < body.ChildCount(); i++ {
		c := body.Child(i)
		var fdecl *sitter.Node
		switch c.Kind() {
		case "function_definition", "field_declaration":
			fdecl = findFunctionDeclarator(c.ChildByFieldName("declarator"))
		}
		if fdecl == nil {
			continue
		}
		if leaf := declaratorLeafName(fdecl.ChildByFieldName("declarator"), src); leaf != "" {
			methods[leaf] = true
		}
	}
	return methods
}

// isScopedEnum reports whether an enum_specifier is "enum class" / "enum struct".
func isScopedEnum(node *sitter.Node, src []byte) bool {
	for i := uint(0); i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c.IsNamed() {
			continue
		}
		switch nodeText(c, src) {
		case "class", "struct":
			return true
		}
	}
	return false
}

// declTypePrefix returns the type-field text of a declaration/field_declaration
// (used to detect const data members).
func declTypePrefix(node *sitter.Node, src []byte) string {
	if t := node.ChildByFieldName("type"); t != nil {
		// Include any leading type_qualifier siblings (const).
		return string(src[node.StartByte():t.EndByte()])
	}
	return ""
}

// simpleTypeName extracts the simple (rightmost, generics-stripped) type name from
// a type node.
func simpleTypeName(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	s := nodeText(node, src)
	// Strip template arguments.
	if idx := strings.IndexByte(s, '<'); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	// Keep the rightmost "::" component.
	if idx := strings.LastIndex(s, "::"); idx >= 0 {
		s = s[idx+2:]
	}
	return strings.TrimSpace(s)
}

func findChildByKind(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		if c := node.Child(i); c.Kind() == kind {
			return c
		}
	}
	return nil
}

func firstNamedChild(node *sitter.Node) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		if c := node.Child(i); c.IsNamed() {
			return c
		}
	}
	return nil
}

func findFirstIdentifier(node *sitter.Node, src []byte) *sitter.Node {
	if node == nil {
		return nil
	}
	switch node.Kind() {
	case "identifier", "field_identifier", "type_identifier":
		return node
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		c := node.Child(i)
		if !c.IsNamed() {
			continue
		}
		if found := findFirstIdentifier(c, src); found != nil {
			return found
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

func isCapitalized(s string) bool {
	if s == "" {
		return false
	}
	return unicode.IsUpper([]rune(s)[0])
}

// isTypeName reports whether s looks like a constructor/type name (capitalized
// identifier), filtering out junk before it becomes an instantiation edge.
func isTypeName(s string) bool {
	if !isCapitalized(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// systemNamespaces are scope roots whose qualified calls are external and should
// not become RelCalls edges.
var systemNamespaces = map[string]bool{
	"std": true, "__gnu_cxx": true, "detail": true,
}

// cppBuiltins are global/standard-library functions that appear as bare calls.
// Resolving them would create dangling phantom edges, so they are suppressed.
var cppBuiltins = map[string]bool{
	"printf": true, "fprintf": true, "sprintf": true, "snprintf": true, "scanf": true,
	"malloc": true, "calloc": true, "realloc": true, "free": true,
	"memcpy": true, "memmove": true, "memset": true, "memcmp": true,
	"strlen": true, "strcmp": true, "strncmp": true, "strcpy": true, "strncpy": true,
	"strcat": true, "strstr": true, "strchr": true, "atoi": true, "atof": true,
	"abs": true, "fabs": true, "min": true, "max": true, "sqrt": true, "pow": true,
	"sin": true, "cos": true, "tan": true, "exp": true, "log": true, "floor": true, "ceil": true,
	"assert": true, "exit": true, "abort": true, "move": true, "forward": true,
	"sizeof": true, "static_cast": true, "dynamic_cast": true, "reinterpret_cast": true,
	"const_cast": true, "make_pair": true, "make_shared": true, "make_unique": true,
}

// cppBuiltinTypes are capitalized names that look like constructors but are
// builtins/macros, so they should not become RelInstantiates edges.
var cppBuiltinTypes = map[string]bool{
	"T": true, "U": true, "K": true, "V": true,
}
