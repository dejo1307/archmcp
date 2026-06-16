package rubyextractor

import (
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	sitter "github.com/tree-sitter/go-tree-sitter"
	ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
)

// extractFileAST parses a Ruby file with tree-sitter and emits architectural
// facts. It replaces the former line-based regex scanner: every symbol, import,
// mixin, constant, attr, ActiveRecord storage/association, and RelCalls edge the
// regex produced is preserved here, with higher fidelity (heredocs, multi-line
// expressions, endless methods, and nested scopes are handled by the grammar).
func extractFileAST(src []byte, relFile string, isRails, exportedByPackwerk bool) []facts.Fact {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(ruby.Language())); err != nil {
		return nil
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()

	w := &rubyWalker{
		src:                src,
		relFile:            relFile,
		dir:                filepath.Dir(relFile),
		isRails:            isRails,
		exportedByPackwerk: exportedByPackwerk,
	}
	w.walkBody(tree.RootNode())
	return w.out
}

// rubyScope tracks a class/module/eigenclass nesting level.
type rubyScope struct {
	name       string // simple (last) name; "" for an eigenclass (class << self)
	kind       string // "class", "module", or "eigenclass"
	visibility string // "public" | "private" | "protected"
	moduleFunc bool   // module_function active: subsequent defs are class methods
	isModel    bool   // ActiveRecord model: associations/scopes/table_name apply
}

type rubyWalker struct {
	src                []byte
	relFile            string
	dir                string
	isRails            bool
	exportedByPackwerk bool

	out        []facts.Fact
	scopeStack []rubyScope
}

// --- scope helpers ---

func (w *rubyWalker) push(s rubyScope) { w.scopeStack = append(w.scopeStack, s) }
func (w *rubyWalker) pop()             { w.scopeStack = w.scopeStack[:len(w.scopeStack)-1] }

func (w *rubyWalker) cur() *rubyScope {
	if len(w.scopeStack) == 0 {
		return nil
	}
	return &w.scopeStack[len(w.scopeStack)-1]
}

// scopeQual joins the enclosing class/module names into a Ruby-qualified name.
// Eigenclass and anonymous entries do not contribute.
func (w *rubyWalker) scopeQual() string {
	var parts []string
	for _, s := range w.scopeStack {
		if s.kind == "eigenclass" || s.name == "" {
			continue
		}
		parts = append(parts, s.name)
	}
	return strings.Join(parts, "::")
}

// curVisibility returns the visibility of the innermost type scope.
func (w *rubyWalker) curVisibility() string {
	if s := w.cur(); s != nil && s.visibility != "" {
		return s.visibility
	}
	return "public"
}

// inEigenclass reports whether the innermost scope is an eigenclass.
func (w *rubyWalker) inEigenclass() bool {
	s := w.cur()
	return s != nil && s.kind == "eigenclass"
}

func (w *rubyWalker) exported() bool {
	return w.curVisibility() == "public" && w.exportedByPackwerk
}

// --- body walking ---

// walkBody iterates the statements of a program or body_statement, dispatching
// each. It is the single entry point for both top-level and nested scopes.
func (w *rubyWalker) walkBody(node *sitter.Node) {
	if node == nil {
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		w.walkStatement(node.Child(i))
	}
}

func (w *rubyWalker) walkStatement(node *sitter.Node) {
	if node == nil || !node.IsNamed() {
		return
	}
	switch node.Kind() {
	case "module":
		w.handleModule(node)
	case "class":
		w.handleClass(node)
	case "singleton_class":
		w.handleSingletonClass(node)
	case "method":
		w.handleMethod(node, w.classMethodContext())
	case "singleton_method":
		w.handleMethod(node, true)
	case "assignment":
		w.handleAssignment(node)
	case "call":
		w.handleBodyCall(node)
		// Descend into a trailing do/brace block so declarations inside
		// included/class_methods/concerning/configure blocks are captured (the
		// former line-based scanner was block-agnostic).
		if body := blockBody(node); body != nil {
			w.walkBody(body)
		}
	case "identifier":
		// Bare statements: visibility markers and module_function.
		switch rubyText(node, w.src) {
		case "private", "protected", "public":
			if s := w.cur(); s != nil {
				s.visibility = rubyText(node, w.src)
			}
		case "module_function":
			if s := w.cur(); s != nil {
				s.moduleFunc = true
			}
		}
	case "comment":
		// ignore
	default:
		// Control-flow / grouping containers (if, unless, begin, case, while,
		// modifiers, ...): descend so nested require/include/def/const
		// declarations are captured, as the line-based scanner did.
		for i := uint(0); i < node.ChildCount(); i++ {
			w.walkStatement(node.Child(i))
		}
	}
}

// classMethodContext reports whether a plain `def` in the current scope should be
// treated as a class method (eigenclass body or after module_function).
func (w *rubyWalker) classMethodContext() bool {
	if w.inEigenclass() {
		return true
	}
	if s := w.cur(); s != nil && s.moduleFunc {
		return true
	}
	return false
}

// --- modules / classes ---

func (w *rubyWalker) handleModule(node *sitter.Node) {
	name := w.constName(node.ChildByFieldName("name"))
	if name == "" {
		return
	}
	qual := w.qualify(name)
	body := node.ChildByFieldName("body")

	props := map[string]any{
		"symbol_kind": facts.SymbolInterface,
		"exported":    w.exportedByPackwerk,
		"language":    "ruby",
	}
	if bodyHasConcern(body, w.src) {
		props["concern"] = true
	}
	if w.isRails {
		props["framework"] = "rails"
	}
	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      qual,
		File:      w.relFile,
		Line:      line(node),
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})

	w.push(rubyScope{name: name, kind: "module", visibility: "public"})
	w.walkBody(body)
	w.pop()
}

func (w *rubyWalker) handleClass(node *sitter.Node) {
	name := w.constName(node.ChildByFieldName("name"))
	if name == "" {
		return
	}
	qual := w.qualify(name)
	superclass := w.superclassName(node.ChildByFieldName("superclass"))

	props := map[string]any{
		"symbol_kind": facts.SymbolClass,
		"exported":    w.exported(),
		"language":    "ruby",
	}
	if w.isRails {
		props["framework"] = "rails"
	}
	if superclass != "" {
		props["superclass"] = superclass
	}
	rels := []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}}
	if superclass != "" {
		rels = append(rels, facts.Relation{Kind: facts.RelImplements, Target: superclass})
	}
	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      qual,
		File:      w.relFile,
		Line:      line(node),
		Props:     props,
		Relations: rels,
	})

	// ActiveRecord model: emit a storage fact and flag the scope so the body
	// scan picks up associations, scopes, and explicit table names.
	isModel := isARBaseClass(superclass)
	if isModel {
		w.out = append(w.out, facts.Fact{
			Kind: facts.KindStorage,
			Name: qual,
			File: w.relFile,
			Line: line(node),
			Props: map[string]any{
				"storage_kind": "model",
				"table":        inferTableName(qual),
				"language":     "ruby",
				"framework":    "rails",
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
		})
	}

	w.push(rubyScope{name: name, kind: "class", visibility: "public", isModel: isModel})
	w.walkBody(node.ChildByFieldName("body"))
	w.pop()
}

func (w *rubyWalker) handleSingletonClass(node *sitter.Node) {
	// class << self — methods inside become class (singleton) methods. The
	// eigenclass entry carries no name and does not affect qualification.
	w.push(rubyScope{name: "", kind: "eigenclass", visibility: "public"})
	w.walkBody(node.ChildByFieldName("body"))
	w.pop()
}

// --- methods ---

func (w *rubyWalker) handleMethod(node *sitter.Node, isClassMethod bool) {
	name := rubyText(node.ChildByFieldName("name"), w.src)
	if name == "" {
		return
	}

	scope := w.scopeQual()
	var fullName string
	switch {
	case scope == "":
		fullName = w.dir + "." + name
	case isClassMethod:
		fullName = scope + "." + name
	default:
		fullName = scope + "#" + name
	}

	symbolKind := facts.SymbolMethod
	if isClassMethod {
		symbolKind = facts.SymbolFunc
	}
	props := map[string]any{
		"symbol_kind": symbolKind,
		"exported":    w.exported(),
		"language":    "ruby",
	}
	if w.isRails {
		props["framework"] = "rails"
	}

	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      fullName,
		File:      w.relFile,
		Line:      line(node),
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})
	ownerIdx := len(w.out) - 1

	// Accumulate RelCalls from the body onto this method (deduplicated).
	seen := make(map[string]bool)
	w.walkForCalls(node.ChildByFieldName("body"), ownerIdx, seen)
}

// walkForCalls recursively scans a method body for call expressions and appends
// RelCalls edges to the owner fact. It does not descend into nested
// method/class/module definitions — those receive their own owner.
func (w *rubyWalker) walkForCalls(node *sitter.Node, ownerIdx int, seen map[string]bool) {
	if node == nil {
		return
	}
	switch node.Kind() {
	case "method", "singleton_method", "class", "module", "singleton_class":
		return
	case "call":
		if target := w.callTarget(node); target != "" && !seen[target] {
			seen[target] = true
			w.out[ownerIdx].Relations = append(w.out[ownerIdx].Relations,
				facts.Relation{Kind: facts.RelCalls, Target: target})
		}
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		w.walkForCalls(node.Child(i), ownerIdx, seen)
	}
}

// callTarget resolves a call node to a RelCalls target string, preserving the
// two-tier convention of the former regex scanner:
//   - constant / scope-resolution receiver  -> "Const.method" / "Ns::Class.method"
//     (always emitted, parentheses not required)
//   - lowercase variable receiver with args -> "var.method"
//     (only when arguments are present, to skip attribute reads)
//
// Bare calls without a receiver are not emitted (matching the regex behavior).
func (w *rubyWalker) callTarget(node *sitter.Node) string {
	recv := node.ChildByFieldName("receiver")
	method := node.ChildByFieldName("method")
	if recv == nil || method == nil {
		return ""
	}
	methodName := rubyText(method, w.src)
	if methodName == "" {
		return ""
	}

	switch recv.Kind() {
	case "constant", "scope_resolution":
		return rubyText(recv, w.src) + "." + methodName
	case "identifier":
		if node.ChildByFieldName("arguments") != nil {
			return rubyText(recv, w.src) + "." + methodName
		}
	case "call":
		// Chained call, e.g. Rails.logger.info(x): use the inner call's method
		// name as a pseudo-receiver when it is a lowercase identifier.
		inner := recv.ChildByFieldName("method")
		if inner != nil && inner.Kind() == "identifier" && node.ChildByFieldName("arguments") != nil {
			return rubyText(inner, w.src) + "." + methodName
		}
	}
	return ""
}

// --- assignments (constants and explicit table names) ---

func (w *rubyWalker) handleAssignment(node *sitter.Node) {
	left := node.ChildByFieldName("left")
	if left == nil {
		return
	}

	// CONSTANT = ... (all-caps only, matching the former regex).
	if left.Kind() == "constant" {
		constName := rubyText(left, w.src)
		if !isAllCaps(constName) {
			return
		}
		scope := w.scopeQual()
		var fullName string
		if scope != "" {
			fullName = scope + "::" + constName
		} else {
			fullName = w.dir + "." + constName
		}
		w.out = append(w.out, facts.Fact{
			Kind: facts.KindSymbol,
			Name: fullName,
			File: w.relFile,
			Line: line(node),
			Props: map[string]any{
				"symbol_kind": facts.SymbolConstant,
				"exported":    w.exported(),
				"language":    "ruby",
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
		})
		return
	}

	// self.table_name = "foo" on an ActiveRecord model.
	if s := w.cur(); s != nil && s.isModel && left.Kind() == "call" {
		if rubyText(left, w.src) == "self.table_name" {
			if tbl := firstStringArg(node.ChildByFieldName("right"), w.src); tbl != "" {
				w.out = append(w.out, facts.Fact{
					Kind: facts.KindStorage,
					Name: tbl,
					File: w.relFile,
					Line: line(node),
					Props: map[string]any{
						"storage_kind": "table",
						"language":     "ruby",
						"framework":    "rails",
						"explicit":     true,
					},
				})
			}
		}
	}
}

// --- body-level DSL calls ---

func (w *rubyWalker) handleBodyCall(node *sitter.Node) {
	// Only bare method calls (no receiver) are DSL declarations.
	if node.ChildByFieldName("receiver") != nil {
		return
	}
	method := rubyText(node.ChildByFieldName("method"), w.src)
	args := node.ChildByFieldName("arguments")

	switch method {
	case "require", "require_relative":
		path := firstStringArg(args, w.src)
		if path == "" {
			return
		}
		props := map[string]any{"language": "ruby"}
		if method == "require_relative" {
			props["require_relative"] = true
		}
		w.out = append(w.out, facts.Fact{
			Kind:      facts.KindDependency,
			Name:      w.dir + " -> " + path,
			File:      w.relFile,
			Line:      line(node),
			Props:     props,
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: path}},
		})

	case "include", "extend", "prepend":
		mixin := firstConstArg(args, w.src)
		if mixin == "" || mixin == "ActiveSupport::Concern" {
			return
		}
		scope := w.scopeQual()
		if scope == "" {
			scope = w.dir
		}
		w.out = append(w.out, facts.Fact{
			Kind: facts.KindDependency,
			Name: scope + " -> " + mixin,
			File: w.relFile,
			Line: line(node),
			Props: map[string]any{
				"language":   "ruby",
				"mixin_kind": method,
			},
			Relations: []facts.Relation{{Kind: facts.RelImplements, Target: mixin}},
		})

	case "attr_reader", "attr_writer", "attr_accessor":
		attrKind := strings.TrimPrefix(method, "attr_")
		scope := w.scopeQual()
		if scope == "" {
			scope = w.dir
		}
		for _, attr := range symbolArgs(args, w.src) {
			w.out = append(w.out, facts.Fact{
				Kind: facts.KindSymbol,
				Name: scope + "#" + attr,
				File: w.relFile,
				Line: line(node),
				Props: map[string]any{
					"symbol_kind": facts.SymbolVariable,
					"exported":    w.exported(),
					"language":    "ruby",
					"attr_kind":   attrKind,
				},
				Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
			})
		}

	case "openapi_spec_path":
		spec := firstStringArg(args, w.src)
		if spec == "" {
			return
		}
		scope := w.scopeQual()
		if scope == "" {
			scope = w.dir
		}
		w.out = append(w.out, facts.Fact{
			Kind: facts.KindDependency,
			Name: scope + " -> " + spec,
			File: w.relFile,
			Line: line(node),
			Props: map[string]any{
				"language":  "ruby",
				"type":      "openapi_spec",
				"spec_file": spec,
			},
			Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: spec}},
		})

	case "has_many", "has_one", "belongs_to", "has_and_belongs_to_many":
		if s := w.cur(); s == nil || !s.isModel {
			return
		}
		assoc := firstSymbolArg(args, w.src)
		if assoc == "" {
			return
		}
		target := assoc
		if method == "has_many" || method == "has_and_belongs_to_many" {
			target = singularize(assoc)
		}
		target = snakeToCamel(target)
		w.out = append(w.out, facts.Fact{
			Kind: facts.KindDependency,
			Name: w.relFile + ":" + method + " :" + assoc,
			File: w.relFile,
			Line: line(node),
			Props: map[string]any{
				"language":         "ruby",
				"association_kind": method,
			},
			Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: target}},
		})

	case "scope":
		if s := w.cur(); s == nil || !s.isModel {
			return
		}
		name := firstSymbolArg(args, w.src)
		if name == "" {
			return
		}
		w.out = append(w.out, facts.Fact{
			Kind: facts.KindSymbol,
			Name: "scope:" + name,
			File: w.relFile,
			Line: line(node),
			Props: map[string]any{
				"symbol_kind": facts.SymbolFunc,
				"language":    "ruby",
				"scope":       true,
			},
		})
	}
}

// --- AST value helpers ---

// qualify joins the current scope with a simple name to form a Ruby-qualified name.
func (w *rubyWalker) qualify(name string) string {
	if scope := w.scopeQual(); scope != "" {
		return scope + "::" + name
	}
	return name
}

// constName returns the name of a constant or scope_resolution node (e.g. "Foo"
// or "Foo::Bar").
func (w *rubyWalker) constName(node *sitter.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "constant", "scope_resolution":
		return rubyText(node, w.src)
	}
	return ""
}

// superclassName extracts the superclass name from a `superclass` node
// (the "< Base" part of a class declaration).
func (w *rubyWalker) superclassName(node *sitter.Node) string {
	if node == nil {
		return ""
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		c := node.Child(i)
		switch c.Kind() {
		case "constant", "scope_resolution":
			return rubyText(c, w.src)
		}
	}
	return ""
}

// bodyHasConcern reports whether a class/module body directly contains
// `extend ActiveSupport::Concern`.
func bodyHasConcern(body *sitter.Node, src []byte) bool {
	if body == nil {
		return false
	}
	for i := uint(0); i < body.ChildCount(); i++ {
		c := body.Child(i)
		if c.Kind() != "call" || c.ChildByFieldName("receiver") != nil {
			continue
		}
		if rubyText(c.ChildByFieldName("method"), src) != "extend" {
			continue
		}
		if firstConstArg(c.ChildByFieldName("arguments"), src) == "ActiveSupport::Concern" {
			return true
		}
	}
	return false
}

// firstStringArg returns the content of the first string in an argument_list (or
// the string node itself when passed directly).
func firstStringArg(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	var find func(n *sitter.Node) *sitter.Node
	find = func(n *sitter.Node) *sitter.Node {
		if n.Kind() == "string" {
			return n
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if r := find(n.Child(i)); r != nil {
				return r
			}
		}
		return nil
	}
	s := find(node)
	if s == nil {
		return ""
	}
	for i := uint(0); i < s.ChildCount(); i++ {
		if s.Child(i).Kind() == "string_content" {
			return rubyText(s.Child(i), src)
		}
	}
	return ""
}

// firstConstArg returns the first constant / scope_resolution argument's text.
func firstConstArg(args *sitter.Node, src []byte) string {
	if args == nil {
		return ""
	}
	for i := uint(0); i < args.ChildCount(); i++ {
		c := args.Child(i)
		switch c.Kind() {
		case "constant", "scope_resolution":
			return rubyText(c, src)
		}
	}
	return ""
}

// symbolArgs returns the names (without leading ':') of all simple_symbol args.
func symbolArgs(args *sitter.Node, src []byte) []string {
	var out []string
	if args == nil {
		return out
	}
	for i := uint(0); i < args.ChildCount(); i++ {
		c := args.Child(i)
		if c.Kind() == "simple_symbol" {
			out = append(out, strings.TrimPrefix(rubyText(c, src), ":"))
		}
	}
	return out
}

// firstSymbolArg returns the first simple_symbol name (without leading ':').
func firstSymbolArg(args *sitter.Node, src []byte) string {
	for _, s := range symbolArgs(args, src) {
		return s
	}
	return ""
}

// isAllCaps reports whether s is an ALL_CAPS constant name (letters uppercase,
// digits and underscores allowed). Matches the former constant regex.
func isAllCaps(s string) bool {
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

// line returns the 1-based start line of a node.
func line(node *sitter.Node) int {
	return int(node.StartPosition().Row) + 1
}

// rubyText returns the source text covered by a node (nil-safe).
func rubyText(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	return string(src[node.StartByte():node.EndByte()])
}
