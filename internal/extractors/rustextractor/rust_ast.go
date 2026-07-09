package rustextractor

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/enola-labs/enola/internal/facts"
	sitter "github.com/tree-sitter/go-tree-sitter"
	rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

// extractFileAST parses a Rust file with tree-sitter and emits architectural
// facts: module-directory-scoped symbols (functions, methods, structs, enums,
// traits, type aliases, consts/statics), dependency facts for `use`
// declarations, RelCalls/RelInstantiates relations, and per-function
// cyclomatic complexity. impl-trait observations are returned separately (see
// implPair) because attaching them requires the full, merged fact set.
func extractFileAST(src []byte, relFile string, crates []crateInfo, moduleDirs map[string]bool) ([]facts.Fact, []implPair) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(rust.Language())); err != nil {
		return nil, nil
	}

	tree := parser.Parse(src, nil)
	defer tree.Close()

	dir := filepath.ToSlash(filepath.Dir(relFile))
	w := &astWalker{
		src:        src,
		relFile:    relFile,
		dir:        dir,
		crateDir:   nearestCrateDir(dir, crates),
		crates:     crates,
		moduleDirs: moduleDirs,
	}
	w.walkSourceFile(tree.RootNode())
	return w.out, w.impls
}

type astWalker struct {
	src      []byte
	relFile  string
	dir      string
	crateDir string

	crates     []crateInfo
	moduleDirs map[string]bool

	// out is the accumulating fact list; impls collects impl-Trait-for-Type
	// observations resolved later by applyImplements.
	out   []facts.Fact
	impls []implPair

	// ownerStack[len-1] points at the symbol fact currently being constructed.
	// Calls/instantiations discovered while walking that symbol's body are
	// appended to its Relations slice.
	ownerStack []*facts.Fact

	// modStack/typeStack hold the enclosing inline-`mod { }` and
	// impl/trait-block names, so a nested declaration's canonical name is
	// "<dir>.<mod1>.<mod2>...<Type>.<name>" — the same qualification scheme
	// the Kotlin/Java extractors use for nested types. methodStack is
	// parallel to typeStack: the set of method names declared directly in the
	// innermost enclosing impl/trait block, used to resolve a same-block
	// sibling call.
	modStack    []string
	typeStack   []string
	methodStack []map[string]bool

	// decisions counts cyclomatic decision points in the function/method body
	// currently being walked; saved/restored around nested function items.
	decisions int
}

// calleeForm classifies how a call_expression's callee was written, so the
// walker knows which resolution strategy is safe to apply.
type calleeForm int

const (
	calleeBare    calleeForm = iota // foo() — same-module lexical scope
	calleeSelfRef                   // self.foo() / Self::foo() / Type::foo() (Type == enclosing impl type)
	calleeOther                     // recv.foo() / other::path::foo() — receiver/path type unknown
)

func (w *astWalker) qualify(name string) string {
	parts := make([]string, 0, len(w.modStack)+len(w.typeStack)+1)
	parts = append(parts, w.modStack...)
	parts = append(parts, w.typeStack...)
	parts = append(parts, name)
	return strings.Join(parts, ".")
}

func (w *astWalker) pushOwner(f *facts.Fact) { w.ownerStack = append(w.ownerStack, f) }
func (w *astWalker) popOwner()               { w.ownerStack = w.ownerStack[:len(w.ownerStack)-1] }
func (w *astWalker) currentOwner() *facts.Fact {
	if len(w.ownerStack) == 0 {
		return nil
	}
	return w.ownerStack[len(w.ownerStack)-1]
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

func (w *astWalker) walkSourceFile(root *sitter.Node) {
	for i := uint(0); i < uint(root.ChildCount()); i++ {
		w.walkChild(root.Child(i))
	}
}

// walkChild dispatches an item declaration to its own handler (creating a new
// owner/type scope as needed) or, for anything else, falls through to
// walkForCalls so calls/decisions nested in ordinary statements are still
// found. Used uniformly at file scope, inside mod/impl/trait bodies, and for
// item declarations nested inside a function body (Rust allows local `fn`,
// `struct`, etc.).
func (w *astWalker) walkChild(c *sitter.Node) {
	switch c.Kind() {
	case "use_declaration":
		w.handleUse(c)
	case "extern_crate_declaration":
		w.handleExternCrate(c)
	case "mod_item":
		w.handleMod(c)
	case "struct_item":
		w.handleStruct(c)
	case "enum_item":
		w.handleEnum(c)
	case "trait_item":
		w.handleTrait(c)
	case "impl_item":
		w.handleImpl(c)
	case "function_item":
		w.handleFunction(c)
	case "function_signature_item":
		w.handleFunctionSignature(c)
	case "type_item":
		w.handleTypeAlias(c)
	case "const_item":
		w.handleConstOrStatic(c, facts.SymbolConstant)
	case "static_item":
		w.handleConstOrStatic(c, facts.SymbolVariable)
	default:
		w.walkForCalls(c)
	}
}

func (w *astWalker) handleMod(node *sitter.Node) {
	body := node.ChildByFieldName("body")
	if body == nil {
		return // `mod foo;` declares another file, parsed independently.
	}
	name := ""
	if n := node.ChildByFieldName("name"); n != nil {
		name = nodeText(n, w.src)
	}
	w.modStack = append(w.modStack, name)
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		w.walkChild(body.Child(i))
	}
	w.modStack = w.modStack[:len(w.modStack)-1]
}

func isExported(node *sitter.Node) bool {
	return findChildByKind(node, "visibility_modifier") != nil
}

func (w *astWalker) handleStruct(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)
	w.out = append(w.out, facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + w.qualify(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolStruct,
			"exported":    isExported(node),
			"language":    "rust",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})
}

func (w *astWalker) handleEnum(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)
	w.out = append(w.out, facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + w.qualify(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolEnum,
			"exported":    isExported(node),
			"language":    "rust",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})
}

func (w *astWalker) handleTrait(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)
	w.out = append(w.out, facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + w.qualify(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolInterface,
			"exported":    isExported(node),
			"language":    "rust",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})

	body := node.ChildByFieldName("body")
	w.pushType(name, collectFnNames(body, w.src))
	if body != nil {
		for i := uint(0); i < uint(body.ChildCount()); i++ {
			w.walkChild(body.Child(i))
		}
	}
	w.popType()
}

func (w *astWalker) handleImpl(node *sitter.Node) {
	typeNode := node.ChildByFieldName("type")
	typeName := simpleTypeName(typeNode, w.src)
	if typeName == "" {
		return
	}
	if traitNode := node.ChildByFieldName("trait"); traitNode != nil {
		if traitName := simpleTypeName(traitNode, w.src); traitName != "" {
			w.impls = append(w.impls, implPair{
				typeName:  w.dir + "." + w.qualify(typeName),
				traitName: traitName,
			})
		}
	}

	body := node.ChildByFieldName("body")
	w.pushType(typeName, collectFnNames(body, w.src))
	if body != nil {
		for i := uint(0); i < uint(body.ChildCount()); i++ {
			w.walkChild(body.Child(i))
		}
	}
	w.popType()
}

func (w *astWalker) handleFunction(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)

	symbolKind := facts.SymbolFunc
	if len(w.typeStack) > 0 {
		symbolKind = facts.SymbolMethod
	}

	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + w.qualify(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": symbolKind,
			"exported":    isExported(node),
			"language":    "rust",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	}
	if len(w.typeStack) > 0 {
		f.Props["receiver"] = w.typeStack[len(w.typeStack)-1]
		if !hasSelfParam(node.ChildByFieldName("parameters")) {
			f.Props["static"] = true
		}
	}

	w.out = append(w.out, f)
	ownerIdx := len(w.out) - 1
	w.pushOwner(&w.out[ownerIdx])

	savedDecisions := w.decisions
	w.decisions = 0
	if body := node.ChildByFieldName("body"); body != nil {
		w.walkForCalls(body)
	}
	w.out[ownerIdx].Props["cyclomatic"] = 1 + w.decisions
	w.decisions = savedDecisions

	w.popOwner()
}

// handleFunctionSignature emits a symbol fact for a trait method declared
// without a body (`fn foo(&self);`) — a real part of the trait's contract,
// but with nothing to walk for calls/complexity.
func (w *astWalker) handleFunctionSignature(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)
	symbolKind := facts.SymbolFunc
	if len(w.typeStack) > 0 {
		symbolKind = facts.SymbolMethod
	}
	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + w.qualify(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": symbolKind,
			"exported":    isExported(node),
			"language":    "rust",
			"cyclomatic":  1,
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	}
	if len(w.typeStack) > 0 {
		f.Props["receiver"] = w.typeStack[len(w.typeStack)-1]
	}
	w.out = append(w.out, f)
}

func (w *astWalker) handleTypeAlias(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)
	w.out = append(w.out, facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + w.qualify(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolType,
			"exported":    isExported(node),
			"language":    "rust",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})
}

func (w *astWalker) handleConstOrStatic(node *sitter.Node, symbolKind string) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)
	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + w.qualify(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": symbolKind,
			"exported":    isExported(node),
			"language":    "rust",
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	}
	w.out = append(w.out, f)
	owner := &w.out[len(w.out)-1]
	w.pushOwner(owner)
	if valueNode := node.ChildByFieldName("value"); valueNode != nil {
		w.walkForCalls(valueNode)
	}
	w.popOwner()
}

func (w *astWalker) handleUse(node *sitter.Node) {
	argNode := node.ChildByFieldName("argument")
	if argNode == nil {
		return
	}
	line := int(node.StartPosition().Row) + 1
	for _, segs := range collectUseItems(argNode, w.src, nil) {
		w.emitDependency(segs, line)
	}
}

func (w *astWalker) handleExternCrate(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	w.emitDependency([]string{nodeText(nameNode, w.src)}, int(node.StartPosition().Row)+1)
}

// emitDependency classifies a `use`/`extern crate` path and emits the
// dependency fact, plus (for a resolvable single-segment item name) an
// importMap-equivalent resolveCall fallback is intentionally NOT populated
// here — see resolveCall's doc comment for why bare-call resolution stays
// local to the enclosing impl/module instead of following imports.
func (w *astWalker) emitDependency(segs []string, line int) {
	if len(segs) == 0 || segs[len(segs)-1] == "" {
		return
	}
	target, source := classifyUsePath(segs, w.dir, w.crateDir, w.crates, w.moduleDirs)
	raw := strings.Join(segs, "::")
	w.out = append(w.out, facts.Fact{
		Kind: facts.KindDependency,
		Name: w.dir + " -> " + raw,
		File: w.relFile,
		Line: line,
		Props: map[string]any{
			"language": "rust",
			"source":   source,
		},
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: target}},
	})
}

// collectUseItems expands a `use` declaration's argument tree into one
// "::"-joined segment list per leaf path, so `use std::{fmt, collections::HashMap};`
// yields two independent paths.
func collectUseItems(node *sitter.Node, src []byte, prefix []string) [][]string {
	if node == nil {
		return nil
	}
	switch node.Kind() {
	case "identifier", "self", "super", "crate", "metavariable":
		return [][]string{appendSegs(prefix, nodeText(node, src))}
	case "scoped_identifier":
		return [][]string{appendSegs(prefix, strings.Split(nodeText(node, src), "::")...)}
	case "use_as_clause":
		if p := node.ChildByFieldName("path"); p != nil {
			return collectUseItems(p, src, prefix)
		}
	case "use_wildcard":
		for i := uint(0); i < uint(node.ChildCount()); i++ {
			if c := node.Child(i); c.IsNamed() {
				items := collectUseItems(c, src, prefix)
				for i2 := range items {
					items[i2] = append(items[i2], "*")
				}
				return items
			}
		}
	case "scoped_use_list":
		newPrefix := prefix
		if p := node.ChildByFieldName("path"); p != nil {
			newPrefix = appendSegs(prefix, strings.Split(nodeText(p, src), "::")...)
		}
		if l := node.ChildByFieldName("list"); l != nil {
			return collectUseItems(l, src, newPrefix)
		}
	case "use_list":
		var out [][]string
		for i := uint(0); i < uint(node.ChildCount()); i++ {
			if c := node.Child(i); c.IsNamed() {
				out = append(out, collectUseItems(c, src, prefix)...)
			}
		}
		return out
	}
	return nil
}

func appendSegs(prefix []string, segs ...string) []string {
	out := make([]string, 0, len(prefix)+len(segs))
	out = append(out, prefix...)
	out = append(out, segs...)
	return out
}

// walkForCalls recursively scans a subtree for cyclomatic decision points and
// call_expression nodes, attributing calls/instantiations to the current
// owner. It dispatches nested item declarations (a local `fn`/`struct`/etc.
// inside a function body, which Rust allows) back through walkChild so they
// get their own owner/type scope instead of being treated as part of the
// enclosing function's body.
func (w *astWalker) walkForCalls(node *sitter.Node) {
	if node == nil {
		return
	}
	switch node.Kind() {
	case "if_expression", "match_arm", "while_expression", "for_expression",
		"loop_expression", "try_expression":
		w.decisions++
	case "binary_expression":
		if rustBooleanOp(node) {
			w.decisions++
		}
	case "call_expression":
		w.handleCallExpression(node)
	case "struct_expression":
		w.handleStructExpression(node)
	}

	for i := uint(0); i < uint(node.ChildCount()); i++ {
		w.walkChild(node.Child(i))
	}
}

// handleStructExpression emits a RelInstantiates edge for a named struct
// literal (`Foo { field: value }`), the idiomatic Rust construction form —
// unlike a tuple-call construction (`Foo()`), it isn't a call_expression, so
// it needs its own case. Field values are walked for nested calls by the
// normal child recursion in walkForCalls, not here.
func (w *astWalker) handleStructExpression(node *sitter.Node) {
	owner := w.currentOwner()
	if owner == nil {
		return
	}
	name := simpleTypeName(node.ChildByFieldName("name"), w.src)
	if name == "" {
		return
	}
	owner.Relations = append(owner.Relations, facts.Relation{Kind: facts.RelInstantiates, Target: name})
}

func (w *astWalker) handleCallExpression(node *sitter.Node) {
	owner := w.currentOwner()
	if owner == nil {
		return
	}
	name, form := w.calleeTrailing(node.ChildByFieldName("function"))
	if name == "" {
		return
	}
	if isCapitalized(name) {
		owner.Relations = append(owner.Relations, facts.Relation{Kind: facts.RelInstantiates, Target: name})
		return
	}
	switch form {
	case calleeBare:
		if target := w.resolveCall(name); target != "" {
			owner.Relations = append(owner.Relations, facts.Relation{Kind: facts.RelCalls, Target: target})
		}
	case calleeSelfRef:
		if methods := w.currentMethods(); methods[name] {
			owner.Relations = append(owner.Relations, facts.Relation{
				Kind:   facts.RelCalls,
				Target: w.dir + "." + w.qualify(name),
			})
		}
	case calleeOther:
		// Receiver/path type is unknown without full type inference. Emitting
		// the bare member name still lets short-name dead-code matching mark
		// the (unqualified) target used, mirroring the Kotlin extractor's
		// navigation_expression fallback.
		owner.Relations = append(owner.Relations, facts.Relation{Kind: facts.RelCalls, Target: name})
	}
}

// resolveCall maps a bare call name to a canonical symbol fact name: a sibling
// method of the enclosing impl/trait block, or a same-directory top-level
// function. Calls to a name reached only through a `use` import are
// deliberately left unresolved (rather than guessing a same-directory
// fallback that could collide with an unrelated local symbol of the same
// name) — precise cross-file bare-call resolution would need the same
// whole-crate module-tree knowledge that submodule `use` resolution already
// approximates in classifyUsePath, which isn't attempted for call sites.
func (w *astWalker) resolveCall(name string) string {
	if methods := w.currentMethods(); methods[name] {
		return w.dir + "." + w.qualify(name)
	}
	if rustBuiltins[name] {
		return ""
	}
	return w.dir + "." + name
}

// calleeTrailing extracts a call_expression's callee simple name and reports
// how it was written, so handleCallExpression knows which resolution strategy
// applies.
func (w *astWalker) calleeTrailing(fn *sitter.Node) (string, calleeForm) {
	if fn == nil {
		return "", calleeOther
	}
	switch fn.Kind() {
	case "identifier":
		return nodeText(fn, w.src), calleeBare
	case "field_expression":
		fieldNode := fn.ChildByFieldName("field")
		if fieldNode == nil || fieldNode.Kind() != "field_identifier" {
			return "", calleeOther
		}
		name := nodeText(fieldNode, w.src)
		if valueNode := fn.ChildByFieldName("value"); valueNode != nil && valueNode.Kind() == "self" {
			return name, calleeSelfRef
		}
		return name, calleeOther
	case "scoped_identifier":
		nameNode := fn.ChildByFieldName("name")
		if nameNode == nil {
			return "", calleeOther
		}
		name := nodeText(nameNode, w.src)
		if pathNode := fn.ChildByFieldName("path"); pathNode != nil {
			pathText := nodeText(pathNode, w.src)
			currentType := ""
			if len(w.typeStack) > 0 {
				currentType = w.typeStack[len(w.typeStack)-1]
			}
			if pathText == "Self" || (currentType != "" && pathText == currentType) {
				return name, calleeSelfRef
			}
		}
		return name, calleeOther
	case "generic_function":
		return w.calleeTrailing(fn.ChildByFieldName("function"))
	default:
		return "", calleeOther
	}
}

// rustBooleanOp reports whether a binary_expression's operator is a logical
// connective — each adds a path for cyclomatic complexity.
func rustBooleanOp(node *sitter.Node) bool {
	op := node.ChildByFieldName("operator")
	if op == nil {
		return false
	}
	switch op.Kind() {
	case "&&", "||":
		return true
	}
	return false
}

// rustBuiltins are free functions always in scope via the Rust prelude. They
// are not project symbols, so resolving them as same-directory calls would
// produce dangling phantom edges.
var rustBuiltins = map[string]bool{
	"drop": true, "panic": true, "print": true, "println": true,
	"format": true, "assert": true, "matches": true,
}

// hasSelfParam reports whether a `parameters` node's first parameter is
// `self`/`&self`/`&mut self`, distinguishing a method from an associated
// (static) function declared in the same impl/trait block.
func hasSelfParam(params *sitter.Node) bool {
	if params == nil {
		return false
	}
	for i := uint(0); i < uint(params.ChildCount()); i++ {
		if params.Child(i).Kind() == "self_parameter" {
			return true
		}
	}
	return false
}

// collectFnNames returns the set of function names declared directly in an
// impl/trait `declaration_list` body (both with and without a body), used to
// resolve same-block sibling calls regardless of declaration order.
func collectFnNames(body *sitter.Node, src []byte) map[string]bool {
	names := make(map[string]bool)
	if body == nil {
		return names
	}
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		c := body.Child(i)
		if c.Kind() != "function_item" && c.Kind() != "function_signature_item" {
			continue
		}
		if n := c.ChildByFieldName("name"); n != nil {
			names[nodeText(n, src)] = true
		}
	}
	return names
}

// simpleTypeName returns a type node's simple (rightmost) identifier, e.g.
// "Wrapper" for "Wrapper<T>" or "MyError" for "&MyError". Used for impl-block
// types/traits, where generics and references must be stripped.
func simpleTypeName(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "type_identifier":
		return nodeText(node, src)
	case "generic_type":
		return simpleTypeName(node.ChildByFieldName("type"), src)
	case "scoped_type_identifier":
		return simpleTypeName(node.ChildByFieldName("name"), src)
	case "reference_type":
		return simpleTypeName(node.ChildByFieldName("type"), src)
	}
	// Fallback: strip references/generics from the raw text.
	t := strings.TrimSpace(nodeText(node, src))
	t = strings.TrimLeft(t, "&")
	t = strings.TrimPrefix(strings.TrimSpace(t), "mut ")
	if i := strings.IndexAny(t, "<("); i >= 0 {
		t = t[:i]
	}
	if i := strings.LastIndex(t, "::"); i >= 0 {
		t = t[i+2:]
	}
	return strings.TrimSpace(t)
}

func isCapitalized(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)[0]
	return unicode.IsUpper(r)
}

// --- tree-sitter helpers ---

func findChildByKind(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		if c := node.Child(i); c.Kind() == kind {
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
