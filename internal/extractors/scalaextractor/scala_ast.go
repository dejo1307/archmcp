package scalaextractor

import (
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	sitter "github.com/tree-sitter/go-tree-sitter"
	scala "github.com/tree-sitter/tree-sitter-scala/bindings/go"
)

// extractFileAST parses one Scala file with tree-sitter and emits its declaration
// symbols, import dependencies, and the type-reference edges visible from this file
// alone. Edge targets are emitted as fully qualified names (resolved through the
// file's own imports and package), which canonicalizeTargets rewrites to canonical
// "<dir>.<Type>" fact names once every file has been merged.
func extractFileAST(src []byte, relFile string, packageIndex map[string]string) []facts.Fact {
	ff, _ := extractFileASTFull(src, relFile, packageIndex)
	return ff
}

// extractFileASTFull additionally returns the file's declared package, which
// canonicalizeTargets needs to resolve a BARE type reference: only the file knows
// which package a name like `Base` would be in, and only the merged fact set knows
// whether such a type exists. Neither half can decide alone, so the walker records
// the package rather than guessing with it (see resolveTypeName).
func extractFileASTFull(src []byte, relFile string, packageIndex map[string]string) ([]facts.Fact, string) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(scala.Language())); err != nil {
		return nil, ""
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()

	w := &astWalker{
		src:          src,
		relFile:      relFile,
		dir:          filepath.ToSlash(filepath.Dir(relFile)),
		packageIndex: packageIndex,
		imports:      map[string]string{},
		ownerStack:   []int{},
	}
	w.walk(tree.RootNode())
	return w.out, w.pkg
}

type astWalker struct {
	src     []byte
	relFile string
	dir     string

	packageIndex map[string]string

	// pkg is the file's package, accumulated across chained `package a.b` /
	// `package c` clauses, which Scala permits and which together name one package.
	pkg string

	// imports maps a simple type name to the FQN it was imported as, so a bare
	// `extends Base` can be resolved the way Java resolves one. A renamed import
	// (`import a.b.{Helper => H}`) is keyed on the LOCAL name, because that is what
	// the reference site writes.
	imports map[string]string

	out []facts.Fact

	// typeStack holds the enclosing type names so a nested declaration is named
	// "<dir>.<Outer>.<Inner>", the same qualification the Java and Kotlin
	// extractors use.
	typeStack []string

	// ownerStack[len-1] indexes into out for the symbol currently being built.
	// An index rather than a pointer: nested declarations append to out, which can
	// reallocate the backing array and strand a raw pointer.
	ownerStack []int

	// inExtension is set while walking an `extension (x: T) def f = …` block, so
	// the methods it contributes are tagged rather than read as free functions.
	inExtension bool
}

// text returns a node's source text.
func (w *astWalker) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	s, e := n.StartByte(), n.EndByte()
	if e > uint(len(w.src)) {
		e = uint(len(w.src))
	}
	return string(w.src[s:e])
}

func (w *astWalker) line(n *sitter.Node) int {
	if n == nil {
		return 0
	}
	return int(n.StartPosition().Row) + 1
}

// walk dispatches on node kind. Anything not recognized is descended into, so a
// declaration nested inside an unhandled expression is still found.
func (w *astWalker) walk(n *sitter.Node) {
	if n == nil {
		return
	}
	switch n.Kind() {
	case "package_clause":
		w.handlePackage(n)
		return
	case "import_declaration":
		w.handleImport(n)
		return
	case "class_definition":
		w.handleTypeLike(n, facts.SymbolClass)
		return
	case "object_definition", "package_object":
		w.handleTypeLike(n, facts.SymbolClass)
		return
	case "trait_definition":
		w.handleTypeLike(n, facts.SymbolInterface)
		return
	case "enum_definition":
		w.handleTypeLike(n, facts.SymbolEnum)
		return
	case "simple_enum_case", "full_enum_case":
		w.handleEnumCase(n)
		return
	case "function_definition", "function_declaration":
		w.handleFunction(n)
		return
	case "val_definition", "val_declaration":
		w.handleValue(n, facts.SymbolConstant)
		return
	case "var_definition", "var_declaration":
		w.handleValue(n, facts.SymbolVariable)
		return
	case "type_definition":
		w.handleTypeAlias(n)
		return
	case "given_definition":
		w.handleGiven(n)
		return
	case "extension_definition":
		prev := w.inExtension
		w.inExtension = true
		w.walkChildren(n)
		w.inExtension = prev
		return
	case "instance_expression":
		// `new Foo(...)` — the one construction form that is unambiguous without
		// type information. Scala's dominant form, `Foo(...)` through a companion
		// `apply`, is syntactically a call and is left to the call-resolution pass.
		w.handleInstanceExpression(n)
		return
	}
	w.walkChildren(n)
}

func (w *astWalker) walkChildren(n *sitter.Node) {
	for i := uint(0); i < n.ChildCount(); i++ {
		w.walk(n.Child(i))
	}
}

// handlePackage accumulates the file's package. Scala allows chained clauses
// (`package com.example` followed by `package model`), which together name
// `com.example.model`, and a clause may carry a body whose declarations belong to
// the package it opens.
func (w *astWalker) handlePackage(n *sitter.Node) {
	if name := n.ChildByFieldName("name"); name != nil {
		part := normalizeDotted(w.text(name))
		if part != "" {
			if w.pkg == "" {
				w.pkg = part
			} else {
				w.pkg += "." + part
			}
		}
	}
	if body := n.ChildByFieldName("body"); body != nil {
		w.walkChildren(body)
		return
	}
	// A clause with no body scopes the rest of the file; its siblings are walked
	// by the caller, so there is nothing further to do here.
}

// handleImport emits one dependency fact per imported name and records the local
// name -> FQN mapping the reference sites resolve through.
//
// Every form the grammar produces is covered: a plain `import a.b.C`, a selector
// list `import a.b.{C, D => E}`, and a wildcard `import a.b._` (Scala 2) or
// `import a.b.*` (Scala 3).
func (w *astWalker) handleImport(n *sitter.Node) {
	var prefix []string
	var selectors []importSelector
	wildcard := false

	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if !c.IsNamed() {
			continue
		}
		switch c.Kind() {
		case "namespace_selectors":
			for j := uint(0); j < c.ChildCount(); j++ {
				s := c.Child(j)
				if !s.IsNamed() {
					continue
				}
				switch s.Kind() {
				case "arrow_renamed_identifier", "renamed_identifier":
					// `Helper => H`: the FQN is built from the ORIGINAL name, the
					// lookup key is the LOCAL one, because that is what the code
					// that uses it writes.
					orig, local := "", ""
					for k := uint(0); k < s.ChildCount(); k++ {
						if id := s.Child(k); id.IsNamed() {
							if orig == "" {
								orig = w.text(id)
							} else {
								local = w.text(id)
							}
						}
					}
					if orig != "" {
						selectors = append(selectors, importSelector{orig: orig, local: firstNonEmpty(local, orig)})
					}
				case "namespace_wildcard":
					wildcard = true
				default:
					t := w.text(s)
					selectors = append(selectors, importSelector{orig: t, local: t})
				}
			}
		case "namespace_wildcard":
			wildcard = true
		default:
			if t := normalizeDotted(w.text(c)); t != "" {
				prefix = append(prefix, t)
			}
		}
	}

	base := strings.Join(prefix, ".")
	if base == "" {
		return
	}

	switch {
	case len(selectors) > 0:
		for _, sel := range selectors {
			fqn := base + "." + sel.orig
			w.imports[sel.local] = fqn
			w.emitDependency(n, fqn, false)
		}
		if wildcard {
			w.emitDependency(n, base, true)
		}
	case wildcard:
		w.emitDependency(n, base, true)
	default:
		// `import a.b.C` — the last segment is the local name.
		if i := strings.LastIndex(base, "."); i >= 0 {
			w.imports[base[i+1:]] = base
		}
		w.emitDependency(n, base, false)
	}
}

type importSelector struct{ orig, local string }

// emitDependency records one import as a dependency fact. `source` is classified
// stdlib here and otherwise left as external; canonicalizeTargets promotes it to
// internal once it can see whether the target is a package this repository
// declares, which no single file can know.
func (w *astWalker) emitDependency(n *sitter.Node, fqn string, wildcard bool) {
	props := map[string]any{
		"import":         fqn,
		"language":       "scala",
		facts.PropSource: depSource(fqn),
	}
	if wildcard {
		props["wildcard"] = true
	}
	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindDependency,
		Name:      w.dir + " -> " + fqn,
		File:      w.relFile,
		Line:      w.line(n),
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: fqn}},
	})
}

// handleTypeLike emits a class / object / trait / enum symbol and walks its body
// with the type pushed onto the stack, so members are named through it.
func (w *astWalker) handleTypeLike(n *sitter.Node, symbolKind string) {
	name := w.text(n.ChildByFieldName("name"))
	if name == "" {
		w.walkChildren(n)
		return
	}

	props := map[string]any{
		"language":    "scala",
		"symbol_kind": symbolKind,
		"exported":    w.isExported(n),
	}
	if fqn := w.fqnFor(name); fqn != "" {
		props["fqn"] = fqn
	}
	// A Scala `object` is a singleton — a class at the bytecode level and a value
	// at the source level. It keeps symbol_kind=class (no new vocabulary) and says
	// which it is in a prop, because "how many classes does this repo have" and
	// "which of them are singletons" are different questions.
	switch n.Kind() {
	case "object_definition":
		props["scala_object"] = true
	case "package_object":
		props["scala_object"] = true
		props["scala_package_object"] = true
	}
	if hasTokenChild(n, "case") {
		props["case_class"] = true
	}
	if mods := w.modifierText(n); mods != "" {
		if strings.Contains(mods, "sealed") {
			props["sealed"] = true
		}
		if strings.Contains(mods, "abstract") {
			props["abstract"] = true
		}
	}

	idx := len(w.out)
	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      w.qualify(name),
		File:      w.relFile,
		Line:      w.line(n),
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})

	w.addSupertypes(idx, n)

	w.typeStack = append(w.typeStack, name)
	w.ownerStack = append(w.ownerStack, idx)
	w.walkBodyAndParams(n)
	w.ownerStack = w.ownerStack[:len(w.ownerStack)-1]
	w.typeStack = w.typeStack[:len(w.typeStack)-1]
}

// walkBodyAndParams walks everything under a type declaration EXCEPT the name and
// the extends clause, which have already been consumed. Class parameters are
// walked so a default-argument expression that constructs something is not lost.
func (w *astWalker) walkBodyAndParams(n *sitter.Node) {
	nameNode := n.ChildByFieldName("name")
	extendNode := n.ChildByFieldName("extend")
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if !c.IsNamed() || sameNode(c, nameNode) {
			continue
		}
		if sameNode(c, extendNode) {
			// The supertype names are already edges; only walk the constructor
			// arguments, which are ordinary expressions.
			if args := c.ChildByFieldName("arguments"); args != nil {
				w.walk(args)
			}
			continue
		}
		w.walk(c)
	}
}

// addSupertypes turns an `extends A with B` clause into implements edges. Scala,
// like C# and unlike Java, does not distinguish extending a class from mixing in a
// trait at the syntax level, and neither does enola's relation vocabulary.
func (w *astWalker) addSupertypes(idx int, n *sitter.Node) {
	ext := n.ChildByFieldName("extend")
	if ext == nil {
		return
	}
	seen := map[string]bool{}
	for i := uint(0); i < ext.ChildCount(); i++ {
		c := ext.Child(i)
		if !c.IsNamed() || c.Kind() == "arguments" {
			continue
		}
		name := w.baseTypeName(c)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		w.out[idx].Relations = append(w.out[idx].Relations,
			facts.Relation{Kind: facts.RelImplements, Target: w.resolveTypeName(name)})
	}
}

// handleEnumCase emits an enum member. `case Active extends Status(1)` also names
// its parent, which is the same implements edge every other declaration produces.
func (w *astWalker) handleEnumCase(n *sitter.Node) {
	name := w.text(n.ChildByFieldName("name"))
	if name == "" {
		// `case Red, Green, Blue` — several names share one node.
		for i := uint(0); i < n.ChildCount(); i++ {
			if c := n.Child(i); c.IsNamed() && c.Kind() == "identifier" {
				w.emitEnumCase(n, w.text(c))
			}
		}
		return
	}
	idx := w.emitEnumCase(n, name)
	if idx >= 0 {
		w.addSupertypes(idx, n)
	}
}

func (w *astWalker) emitEnumCase(n *sitter.Node, name string) int {
	if name == "" {
		return -1
	}
	idx := len(w.out)
	w.out = append(w.out, facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.qualify(name),
		File: w.relFile,
		Line: w.line(n),
		Props: map[string]any{
			"language":    "scala",
			"symbol_kind": facts.SymbolConstant,
			"exported":    true,
			"enum_case":   true,
		},
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})
	return idx
}

// handleFunction emits a def. A def declared inside a type is a method carrying
// its receiver; one at file or package-object scope is a function.
func (w *astWalker) handleFunction(n *sitter.Node) {
	name := w.text(n.ChildByFieldName("name"))
	if name == "" {
		w.walkChildren(n)
		return
	}

	kind := facts.SymbolFunc
	props := map[string]any{
		"language": "scala",
		"exported": w.isExported(n),
	}
	if len(w.typeStack) > 0 {
		kind = facts.SymbolMethod
		props["receiver"] = w.typeStack[len(w.typeStack)-1]
	}
	props["symbol_kind"] = kind
	if n.Kind() == "function_declaration" {
		// No body: an abstract member of a trait or abstract class.
		props["abstract"] = true
	}
	if w.inExtension {
		props["scala_extension"] = true
	}
	if mods := w.modifierText(n); strings.Contains(mods, "implicit") {
		props["implicit"] = true
	}

	idx := len(w.out)
	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      w.qualify(name),
		File:      w.relFile,
		Line:      w.line(n),
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})

	// A def body may declare local types and construct things; walk it with this
	// def as the owner so those edges attach here. Locals nested inside a def do
	// NOT push typeStack beyond the def, so a local class is named through its
	// enclosing type rather than through the method.
	w.ownerStack = append(w.ownerStack, idx)
	w.walkMembers(n, n.ChildByFieldName("name"))
	w.ownerStack = w.ownerStack[:len(w.ownerStack)-1]
}

// handleValue emits a val/var. Scala's `val` is an immutable binding, so it maps
// to constant and `var` to variable — the distinction downstream analyses read.
func (w *astWalker) handleValue(n *sitter.Node, symbolKind string) {
	// val/var bind a PATTERN, not a name: `val (a, b) = pair` is legal. Only a
	// plain identifier yields a symbol; a destructuring binding is skipped rather
	// than guessed at, and its initializer is still walked for edges.
	nameNode := n.ChildByFieldName("pattern")
	if nameNode == nil {
		nameNode = n.ChildByFieldName("name")
	}
	name := ""
	if nameNode != nil && (nameNode.Kind() == "identifier" || nameNode.Kind() == "stable_identifier") {
		name = w.text(nameNode)
	}
	if name == "" {
		w.walkMembers(n, nil)
		return
	}

	props := map[string]any{
		"language":    "scala",
		"symbol_kind": symbolKind,
		"exported":    w.isExported(n),
	}
	if mods := w.modifierText(n); strings.Contains(mods, "implicit") {
		props["implicit"] = true
	}
	if len(w.typeStack) > 0 {
		props["receiver"] = w.typeStack[len(w.typeStack)-1]
	}

	idx := len(w.out)
	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      w.qualify(name),
		File:      w.relFile,
		Line:      w.line(n),
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})

	w.ownerStack = append(w.ownerStack, idx)
	w.walkMembers(n, nameNode)
	w.ownerStack = w.ownerStack[:len(w.ownerStack)-1]
}

// handleTypeAlias emits a `type X = Y` member.
func (w *astWalker) handleTypeAlias(n *sitter.Node) {
	name := w.text(n.ChildByFieldName("name"))
	if name == "" {
		w.walkChildren(n)
		return
	}
	props := map[string]any{
		"language":    "scala",
		"symbol_kind": facts.SymbolType,
		"exported":    w.isExported(n),
	}
	if fqn := w.fqnFor(name); fqn != "" {
		props["fqn"] = fqn
	}
	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      w.qualify(name),
		File:      w.relFile,
		Line:      w.line(n),
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})
}

// handleGiven emits a Scala 3 `given` instance. A given is a value the compiler
// supplies at call sites rather than one anybody names, so it is a variable that
// says what it is in a prop; the type it provides becomes an implements edge,
// which is what makes "who provides this typeclass instance" answerable.
func (w *astWalker) handleGiven(n *sitter.Node) {
	name := w.text(n.ChildByFieldName("name"))
	anonymous := name == ""
	if anonymous {
		// `given Ordering[Color] with …` — an anonymous given is named for the
		// type it provides, which is the only stable identifier it has.
		if rt := n.ChildByFieldName("return_type"); rt != nil {
			name = w.baseTypeName(rt)
		}
		if name == "" {
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c.IsNamed() && isTypeNode(c.Kind()) {
					name = w.baseTypeName(c)
					break
				}
			}
		}
		if name != "" {
			name = "given_" + name
		}
	}
	if name == "" {
		w.walkChildren(n)
		return
	}

	props := map[string]any{
		"language":    "scala",
		"symbol_kind": facts.SymbolVariable,
		"exported":    w.isExported(n),
		"scala_given": true,
	}
	if anonymous {
		props["anonymous"] = true
	}

	idx := len(w.out)
	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      w.qualify(name),
		File:      w.relFile,
		Line:      w.line(n),
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})

	if rt := n.ChildByFieldName("return_type"); rt != nil {
		if t := w.baseTypeName(rt); t != "" {
			w.out[idx].Relations = append(w.out[idx].Relations,
				facts.Relation{Kind: facts.RelImplements, Target: w.resolveTypeName(t)})
		}
	}

	// A `given … with { def … }` body declares real methods; push the given as
	// their enclosing type so they are named and attributed through it.
	w.typeStack = append(w.typeStack, name)
	w.ownerStack = append(w.ownerStack, idx)
	w.walkMembers(n, n.ChildByFieldName("name"))
	w.ownerStack = w.ownerStack[:len(w.ownerStack)-1]
	w.typeStack = w.typeStack[:len(w.typeStack)-1]
}

// handleInstanceExpression records `new Foo(...)` as an instantiates edge on the
// enclosing symbol, then walks the arguments for nested constructions.
func (w *astWalker) handleInstanceExpression(n *sitter.Node) {
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if !c.IsNamed() || c.Kind() == "arguments" {
			continue
		}
		if t := w.baseTypeName(c); t != "" {
			w.addEdge(facts.RelInstantiates, w.resolveTypeName(t))
			break
		}
	}
	if args := n.ChildByFieldName("arguments"); args != nil {
		w.walk(args)
	}
}

// addEdge attaches a relation to the innermost enclosing symbol. An edge with no
// owner — a construction in file-scope code — is dropped rather than hung on an
// arbitrary fact; file-scope reference capture belongs with the call pass.
func (w *astWalker) addEdge(kind, target string) {
	if target == "" || len(w.ownerStack) == 0 {
		return
	}
	idx := w.ownerStack[len(w.ownerStack)-1]
	for _, r := range w.out[idx].Relations {
		if r.Kind == kind && r.Target == target {
			return
		}
	}
	w.out[idx].Relations = append(w.out[idx].Relations, facts.Relation{Kind: kind, Target: target})
}

// walkMembers walks every named child except skip.
func (w *astWalker) walkMembers(n *sitter.Node, skip *sitter.Node) {
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if !c.IsNamed() || sameNode(c, skip) {
			continue
		}
		w.walk(c)
	}
}

// --- naming and resolution helpers ---

// qualify builds the canonical fact name: "<dir>.<Outer>.<Inner>.<member>".
func (w *astWalker) qualify(name string) string {
	parts := make([]string, 0, len(w.typeStack)+1)
	parts = append(parts, w.typeStack...)
	parts = append(parts, name)
	return w.dir + "." + strings.Join(parts, ".")
}

// fqnFor builds the Scala fully qualified name of a declaration — the package plus
// the enclosing type chain. It is what other files' references resolve against,
// and it is frequently NOT derivable from the directory: Scala, like C#, lets a
// package disagree with the file system.
func (w *astWalker) fqnFor(name string) string {
	parts := make([]string, 0, len(w.typeStack)+2)
	if w.pkg != "" {
		parts = append(parts, w.pkg)
	}
	parts = append(parts, w.typeStack...)
	parts = append(parts, name)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ".")
}

// resolveTypeName turns a reference as written into the FQN it names, using the
// file's imports — the one scope a single file can resolve through with certainty.
// An already-dotted reference is taken as written.
//
// A bare name with no matching import is deliberately left BARE rather than
// qualified with the file's own package. Java can make that assumption because its
// imports are explicit; Scala cannot, because it auto-imports `scala.*`,
// `java.lang.*` and `scala.Predef.*`, so a bare `Ordering`, `Seq` or `Exception` is
// far more often stdlib than same-package. Qualifying it would publish an edge to
// `com.example.app.Ordering` — a type in a package that does not declare it — and
// because the graph is name-keyed, that fabricated name would materialize as a
// phantom node. canonicalizeTargets resolves the same-package case afterwards,
// where it can check the merged fact set instead of assuming.
func (w *astWalker) resolveTypeName(name string) string {
	if name == "" {
		return ""
	}
	if strings.Contains(name, ".") {
		return name
	}
	if fqn, ok := w.imports[name]; ok {
		return fqn
	}
	return name
}

// baseTypeName strips type arguments and qualifies down to the type being named:
// `Option[A]` -> `Option`, `scala.collection.Map` -> `scala.collection.Map`.
func (w *astWalker) baseTypeName(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "generic_type":
		// The type constructor is the first named child; the rest are arguments.
		for i := uint(0); i < n.ChildCount(); i++ {
			if c := n.Child(i); c.IsNamed() && c.Kind() != "type_arguments" {
				return w.baseTypeName(c)
			}
		}
		return ""
	case "type_identifier", "identifier":
		return w.text(n)
	case "stable_type_identifier", "stable_identifier", "projected_type":
		return normalizeDotted(w.text(n))
	case "annotated_type", "compound_type", "singleton_type":
		for i := uint(0); i < n.ChildCount(); i++ {
			if c := n.Child(i); c.IsNamed() {
				if t := w.baseTypeName(c); t != "" {
					return t
				}
			}
		}
		return ""
	}
	if isTypeNode(n.Kind()) {
		return normalizeDotted(w.text(n))
	}
	return ""
}

func isTypeNode(kind string) bool {
	switch kind {
	case "type_identifier", "generic_type", "stable_type_identifier",
		"projected_type", "compound_type", "annotated_type", "singleton_type":
		return true
	}
	return false
}

// isExported reports whether a declaration is part of the public surface. Scala
// defaults to public, so only an explicit private/protected removes it. A
// qualified `private[pkg]` is package-private and counted as non-exported, the
// same reading Kotlin's `internal` gets.
func (w *astWalker) isExported(n *sitter.Node) bool {
	mods := w.modifierText(n)
	return !strings.Contains(mods, "private") && !strings.Contains(mods, "protected")
}

// modifierText returns the text of the declaration's `modifiers` node, or "".
func (w *astWalker) modifierText(n *sitter.Node) string {
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Kind() == "modifiers" {
			return w.text(c)
		}
	}
	return ""
}

// sameNode reports whether two node handles refer to the same tree node. Node is a
// value type over a C struct, so handles to one node are distinct Go pointers and
// must be compared by identity rather than by ==.
func sameNode(a, b *sitter.Node) bool {
	return a != nil && b != nil && a.Id() == b.Id()
}

// hasTokenChild reports whether n has a direct child of the given (anonymous)
// token kind — how the grammar represents `case` on a class or object.
func hasTokenChild(n *sitter.Node, kind string) bool {
	for i := uint(0); i < n.ChildCount(); i++ {
		if n.Child(i).Kind() == kind {
			return true
		}
	}
	return false
}

// normalizeDotted collapses whitespace and backticks out of a dotted path, so a
// path split across lines compares equal to one written inline.
func normalizeDotted(s string) string {
	s = strings.ReplaceAll(s, "`", "")
	parts := strings.Split(s, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ".")
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
