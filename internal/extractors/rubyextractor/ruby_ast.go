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
	name         string // simple (last) name; "" for an eigenclass (class << self)
	kind         string // "class", "module", or "eigenclass"
	visibility   string // "public" | "private" | "protected"
	moduleFunc   bool   // module_function active: subsequent defs are class methods
	isModel      bool   // ActiveRecord model: associations/scopes/table_name apply
	isSerializer bool   // ActiveModel::Serializer: attributes/associations back methods
	symFactIdx   int    // index into w.out of this scope's class/module symbol fact, or -1
}

type rubyWalker struct {
	src                []byte
	relFile            string
	dir                string
	isRails            bool
	exportedByPackwerk bool

	out        []facts.Fact
	scopeStack []rubyScope

	// Per-method complexity state, set up by handleMethod around walkForCalls.
	// metrics is nil outside a method body walk. loopDepth is the current loop
	// nesting depth; selfName/selfShort are the enclosing method's full and short
	// names (for direct-recursion detection — Ruby self-calls are usually bare).
	metrics   *rubyBodyMetrics
	loopDepth int
	selfName  string
	selfShort string
}

// rubyBodyMetrics accumulates per-method complexity signals during the single
// walkForCalls body traversal — mirrors the Go/Python extractors.
type rubyBodyMetrics struct {
	loopDepth   int             // max loop nesting depth
	loopCount   int             // number of loop constructs (syntactic + iterator blocks)
	decisions   int             // decision points (cyclomatic = 1 + decisions)
	callsInLoop []string        // distinct call targets invoked at loop depth >= 1
	inLoopSeen  map[string]bool // dedup set for callsInLoop
	recursive   bool            // body directly calls the enclosing method
}

// rubyIterators are methods whose block runs once per element — i.e. a loop.
// Block-taking methods NOT in this set (transaction, tap, synchronize,
// File.open, …) run their block once and are deliberately not treated as loops.
// Aggregate-or-iterate methods (count/sum/find/all?…) are safe to include
// because a block is required before any of these counts as a loop.
var rubyIterators = map[string]bool{
	"each": true, "each_with_index": true, "each_with_object": true,
	"each_pair": true, "each_key": true, "each_value": true,
	"each_slice": true, "each_cons": true, "each_line": true,
	"each_char": true, "each_entry": true,
	"map": true, "map!": true, "collect": true, "collect!": true,
	"flat_map": true, "select": true, "select!": true, "filter": true,
	"filter_map": true, "reject": true, "reject!": true,
	"detect": true, "find": true, "find_all": true, "find_index": true,
	"find_each": true, "find_in_batches": true, "in_batches": true,
	"reduce": true, "inject": true, "min_by": true, "max_by": true,
	"sort_by": true, "group_by": true, "partition": true, "chunk_while": true,
	"zip": true, "cycle": true, "times": true, "upto": true, "downto": true,
	"step": true, "loop": true, "all?": true, "any?": true, "none?": true,
	"one?": true, "count": true, "sum": true, "tally_by": true,
}

// recordCallMetrics notes a resolved call target against the current method's
// complexity metrics: flags direct recursion and records calls made inside loops.
func (w *rubyWalker) recordCallMetrics(target string) {
	if w.metrics == nil || target == "" {
		return
	}
	if target == w.selfShort || target == w.selfName || target == "self."+w.selfShort {
		w.metrics.recursive = true
	}
	w.recordInLoopCall(target)
}

// recordInLoopCall adds a target to calls_in_loop (deduped) when inside a loop,
// without the recursion check — used for raw instance-method names (e.g. an
// association read `u.posts`) whose name must not be mistaken for self-recursion.
func (w *rubyWalker) recordInLoopCall(target string) {
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

// rubyCheapMethods are obviously-cheap attribute/Enumerable/Kernel methods that
// are not DB I/O. No-arg instance calls to these inside loops are not recorded in
// calls_in_loop, to keep it focused (the enterprise association/keyword gate is
// the real precision filter, so this list need not be exhaustive).
var rubyCheapMethods = map[string]bool{
	"id": true, "name": true, "to_s": true, "to_str": true, "to_i": true,
	"to_a": true, "to_h": true, "to_sym": true, "to_param": true, "inspect": true,
	"hash": true, "class": true, "object_id": true, "freeze": true, "frozen?": true,
	"dup": true, "clone": true, "present?": true, "blank?": true, "nil?": true,
	"empty?": true, "any?": true, "size": true, "length": true, "first": true,
	"last": true, "keys": true, "values": true, "key?": true, "include?": true,
	"is_a?": true, "kind_of?": true, "instance_of?": true, "respond_to?": true,
	"tap": true, "then": true, "itself": true, "send": true, "public_send": true,
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

	w.push(rubyScope{name: name, kind: "module", visibility: "public", symFactIdx: len(w.out) - 1})
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
	clsIdx := len(w.out) - 1

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

	w.push(rubyScope{name: name, kind: "class", visibility: "public", isModel: isModel,
		isSerializer: isSerializerBase(superclass), symFactIdx: clsIdx})
	w.walkBody(node.ChildByFieldName("body"))
	w.pop()
}

func (w *rubyWalker) handleSingletonClass(node *sitter.Node) {
	// class << self — methods inside become class (singleton) methods. The
	// eigenclass entry carries no name and does not affect qualification.
	w.push(rubyScope{name: "", kind: "eigenclass", visibility: "public", symFactIdx: -1})
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

	// Accumulate RelCalls from the body onto this method (deduplicated), while
	// computing complexity metrics in the same walk. The props map is shared by
	// reference with the fact just appended, so writing to it after the walk
	// updates the emitted fact.
	seen := make(map[string]bool)
	locals := collectLocals(node, w.src)
	w.metrics = &rubyBodyMetrics{}
	w.loopDepth = 0
	w.selfName = fullName
	w.selfShort = name
	w.walkForCalls(node.ChildByFieldName("body"), ownerIdx, seen, locals)
	props["cyclomatic"] = 1 + w.metrics.decisions
	if w.metrics.loopDepth > 0 {
		props["loop_depth"] = w.metrics.loopDepth
	}
	if w.metrics.loopCount > 0 {
		props["loop_count"] = w.metrics.loopCount
	}
	if len(w.metrics.callsInLoop) > 0 {
		props["calls_in_loop"] = w.metrics.callsInLoop
	}
	if w.metrics.recursive {
		props["recursive_self"] = true
	}
	w.metrics = nil
}

// walkForCalls recursively scans a method body for call expressions and appends
// RelCalls edges to the owner fact. It does not descend into nested
// method/class/module definitions — those receive their own owner.
//
// Four reference shapes are captured: (1) qualified calls via callTarget
// ("Const.method", "var.method"); (2) bare calls with a method name but no
// receiver ("render :x", "helper(arg)") → the bare method name; (3) lone
// identifiers in expression position ("current_user") that are not known locals
// → the bare name; (4) bare constant references ("MyJob", "Chat::Message") used
// as values → the constant name. (2) and (3) are why Ruby methods invoked without
// a receiver (the common Rails case) are recorded as referenced; (4) is why a
// class/module used only as a value (registered, passed as an argument, matched in
// case/when) is. Bare targets — from (2), (3) and (4) — carry no ".", so
// constFromCall ignores them and the package-metrics coupling graph (which keys
// off "Recv.method" constant receivers) is unaffected.
func (w *rubyWalker) walkForCalls(node *sitter.Node, ownerIdx int, seen, locals map[string]bool) {
	if node == nil {
		return
	}
	// Skip anonymous tokens (keywords/operators/punctuation). They are childless
	// leaves, and critically the keyword token for a statement shares its Kind
	// (e.g. the `while`/`if` keyword reports Kind "while"/"if"), which would
	// otherwise double-count loops and decisions below.
	if !node.IsNamed() {
		return
	}

	// Complexity metrics: count decision points so the single body walk doubles
	// as the cyclomatic pass. `case` itself is not counted (each `when` branch is);
	// loop constructs are counted in their own handling below.
	if w.metrics != nil {
		switch node.Kind() {
		case "if", "elsif", "unless", "if_modifier", "unless_modifier",
			"when", "rescue", "conditional":
			w.metrics.decisions++
		}
	}

	switch node.Kind() {
	case "method", "singleton_method", "class", "module", "singleton_class":
		return
	case "while", "until", "for", "while_modifier", "until_modifier":
		// Syntactic loops: everything in the body runs per iteration.
		if w.metrics != nil {
			w.metrics.loopCount++
			w.metrics.decisions++
			if w.loopDepth+1 > w.metrics.loopDepth {
				w.metrics.loopDepth = w.loopDepth + 1
			}
		}
		w.loopDepth++
		for i := uint(0); i < node.ChildCount(); i++ {
			w.walkForCalls(node.Child(i), ownerIdx, seen, locals)
		}
		w.loopDepth--
		return
	case "call":
		method := node.ChildByFieldName("method")
		recv := node.ChildByFieldName("receiver")
		if target := w.callTarget(node); target != "" {
			w.addCall(ownerIdx, seen, target)
			w.recordCallMetrics(target)
		} else if recv == nil && method != nil && method.Kind() == "identifier" {
			if name := rubyText(method, w.src); !rubyNonCalls[name] {
				w.addCall(ownerIdx, seen, name)
				w.recordCallMetrics(name)
			}
		} else if w.loopDepth > 0 && recv != nil && method != nil && method.Kind() == "identifier" {
			// A no-arg instance call inside a loop that callTarget suppressed (e.g.
			// the association read `u.posts` or a `record.reload`). It is not a graph
			// edge, but its method name feeds the perf metric so the enterprise
			// analyzer can flag lazy-loaded association / per-iteration I/O (N+1).
			if name := rubyText(method, w.src); !rubyNonCalls[name] && !rubyCheapMethods[name] {
				w.recordInLoopCall(name)
			}
		}
		// An iterator method with a block (users.each { … }, n.times { … }) is a
		// loop: its block body runs per element, but the receiver and arguments
		// are evaluated once — so only the block child walks at +1 depth (mirrors
		// the Python comprehension handling).
		block := node.ChildByFieldName("block")
		isIter := block != nil && method != nil && rubyIterators[rubyText(method, w.src)]
		if isIter && w.metrics != nil {
			w.metrics.loopCount++
			w.metrics.decisions++
			if w.loopDepth+1 > w.metrics.loopDepth {
				w.metrics.loopDepth = w.loopDepth + 1
			}
		}
		// Recurse into every child EXCEPT the callee `method` child, which has
		// already been consumed above (otherwise it would be re-counted by the
		// bare-identifier case below).
		for i := uint(0); i < node.ChildCount(); i++ {
			c := node.Child(i)
			if method != nil && c.StartByte() == method.StartByte() && c.EndByte() == method.EndByte() {
				continue
			}
			if isIter && c.StartByte() == block.StartByte() && c.EndByte() == block.EndByte() {
				w.loopDepth++
				w.walkForCalls(c, ownerIdx, seen, locals)
				w.loopDepth--
				continue
			}
			w.walkForCalls(c, ownerIdx, seen, locals)
		}
		return
	case "identifier":
		// A bare identifier outside callee position: either an arg-less method
		// call or a local variable read. Emit unless it is a known local or a
		// keyword/builtin; matching is conservative so over-emitting is safe.
		if name := rubyText(node, w.src); name != "" && !locals[name] && !rubyNonCalls[name] {
			w.addCall(ownerIdx, seen, name)
			w.recordCallMetrics(name)
		}
		return
	case "constant", "scope_resolution":
		// A bare constant reference in expression position — an argument
		// (register(MyJob)), array/hash element, case/when or rescue clause,
		// assignment RHS, or a lone `Foo` value. It is NOT a `Const.method` call
		// (that is captured as the receiver via callTarget above) and NOT a
		// definition name (handleClass/handleModule consume those), so without this
		// a class/module used only as a value looks unreferenced and is mis-reported
		// as dead. Record it as a use of that constant. The target carries no ".",
		// so constFromCall ignores it and the package-metrics coupling graph is
		// unaffected; it is not a method invocation, so perf metrics are untouched.
		// scope_resolution is recorded whole (e.g. "Chat::Message") and not
		// descended into, so the qualified path is matched rather than its segments.
		if name := stripLeadingColons(rubyText(node, w.src)); name != "" && !rubyBuiltinConsts[name] {
			w.addCall(ownerIdx, seen, name)
		}
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		w.walkForCalls(node.Child(i), ownerIdx, seen, locals)
	}
}

// addCall appends a deduplicated RelCalls edge to the owner fact.
func (w *rubyWalker) addCall(ownerIdx int, seen map[string]bool, target string) {
	if target == "" || seen[target] {
		return
	}
	seen[target] = true
	w.out[ownerIdx].Relations = append(w.out[ownerIdx].Relations,
		facts.Relation{Kind: facts.RelCalls, Target: target})
}

// addCallToFact appends a RelCalls edge to an arbitrary fact (used for DSL
// references attached to the enclosing class), skipping exact duplicates.
func (w *rubyWalker) addCallToFact(idx int, target string) {
	if target == "" || idx < 0 || idx >= len(w.out) {
		return
	}
	for _, r := range w.out[idx].Relations {
		if r.Kind == facts.RelCalls && r.Target == target {
			return
		}
	}
	w.out[idx].Relations = append(w.out[idx].Relations,
		facts.Relation{Kind: facts.RelCalls, Target: target})
}

// rubyCallbackDSL are Rails class-body methods that take method-name symbol
// arguments (controller filters, ActiveRecord lifecycle callbacks, custom
// validations). `validates` is intentionally excluded — its first symbol is an
// attribute name, not a method.
var rubyCallbackDSL = map[string]bool{
	"before_action": true, "after_action": true, "around_action": true,
	"append_before_action": true, "prepend_before_action": true,
	"skip_before_action": true, "skip_after_action": true, "skip_around_action": true,
	"before_save": true, "after_save": true, "around_save": true,
	"before_create": true, "after_create": true, "around_create": true,
	"before_update": true, "after_update": true, "around_update": true,
	"before_destroy": true, "after_destroy": true, "around_destroy": true,
	"before_validation": true, "after_validation": true,
	"after_commit": true, "after_rollback": true, "after_initialize": true,
	"after_find": true, "after_touch": true,
	"validate": true,
}

// rubySerializerDSL are ActiveModel::Serializer class-body methods whose symbol
// arguments name attributes/associations. Each declared name is backed by an
// optional same-named method (and an `include_<name>?` predicate) that the
// serializer framework invokes — never an explicit Ruby call — so they are folded
// in as references (see handleBodyCall). Applied only inside a serializer class.
var rubySerializerDSL = map[string]bool{
	"attributes": true, "attribute": true,
	"has_one": true, "has_many": true, "belongs_to": true, "has_and_belongs_to_many": true,
}

// rubyBuiltinConsts are Ruby core and common stdlib constants. A bare reference to
// one is real, but emitting a call edge to it inflates fan-in on monkey-patch
// reopenings (Discourse's freedom_patches `class Array`/`String`/`Time`), turning
// uninteresting core classes into spurious god-class / hotspot findings while
// never being a useful dead-code lead. They are skipped when recording bare
// constant references. Namespaced constants (Foo::Array) are unaffected.
var rubyBuiltinConsts = map[string]bool{
	"Object": true, "BasicObject": true, "Module": true, "Class": true, "Method": true,
	"UnboundMethod": true, "Proc": true, "Binding": true, "Data": true,
	"Array": true, "Hash": true, "String": true, "Symbol": true, "Set": true,
	"Integer": true, "Float": true, "Numeric": true, "Rational": true, "Complex": true,
	"Range": true, "Regexp": true, "MatchData": true, "Struct": true, "Enumerator": true,
	"TrueClass": true, "FalseClass": true, "NilClass": true,
	"Time": true, "Date": true, "DateTime": true,
	"Comparable": true, "Enumerable": true, "Kernel": true, "Math": true,
	"IO": true, "File": true, "Dir": true, "FileUtils": true, "Pathname": true,
	"StringIO": true, "Tempfile": true,
	"Thread": true, "Mutex": true, "ConditionVariable": true, "Queue": true,
	"SizedQueue": true, "Fiber": true, "ThreadGroup": true,
	"Exception": true, "StandardError": true, "RuntimeError": true, "ArgumentError": true,
	"TypeError": true, "NameError": true, "NoMethodError": true, "IndexError": true,
	"KeyError": true, "RangeError": true, "IOError": true, "NotImplementedError": true,
	"StopIteration": true, "ZeroDivisionError": true, "FrozenError": true,
	"Marshal": true, "ObjectSpace": true, "GC": true, "Process": true, "Signal": true,
	"Encoding": true, "Random": true, "SecureRandom": true, "Mutex_m": true,
}

// rubyNonCalls are bare identifiers that must not be treated as method-call
// references: keywords/builtins that commonly appear in expression position.
// (Most Ruby keywords — self, nil, super, yield, return — are their own AST node
// kinds and never reach the identifier case, but this guards the rest.)
var rubyNonCalls = map[string]bool{
	"self": true, "nil": true, "true": true, "false": true, "super": true,
	"yield": true, "return": true, "next": true, "break": true, "redo": true,
	"retry": true, "raise": true, "throw": true, "loop": true, "proc": true,
	"lambda": true, "puts": true, "print": true, "p": true, "require": true,
	"require_relative": true, "load": true, "freeze": true, "block_given?": true,
	"__method__": true, "private": true, "protected": true, "public": true,
	"module_function": true,
}

// collectLocals gathers the parameter names and locally-assigned variable names
// of a method so walkForCalls can tell a local variable read apart from an
// arg-less method call.
func collectLocals(method *sitter.Node, src []byte) map[string]bool {
	locals := map[string]bool{}
	if method == nil {
		return locals
	}
	if params := method.ChildByFieldName("parameters"); params != nil {
		collectIdentifiers(params, src, locals)
	}
	collectAssignTargets(method.ChildByFieldName("body"), src, locals)
	return locals
}

// collectIdentifiers adds every identifier name in a subtree to out.
func collectIdentifiers(node *sitter.Node, src []byte, out map[string]bool) {
	if node == nil {
		return
	}
	if node.Kind() == "identifier" {
		out[rubyText(node, src)] = true
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		collectIdentifiers(node.Child(i), src, out)
	}
}

// collectAssignTargets adds the left-hand identifier(s) of every assignment in a
// subtree to out (plain `x = …` and multiple-assignment `a, b = …`; setter
// calls like `self.x = …` are skipped).
func collectAssignTargets(node *sitter.Node, src []byte, out map[string]bool) {
	if node == nil {
		return
	}
	switch node.Kind() {
	case "assignment", "operator_assignment":
		switch left := node.ChildByFieldName("left"); {
		case left == nil:
		case left.Kind() == "identifier":
			out[rubyText(left, src)] = true
		case left.Kind() == "left_assignment_list":
			collectIdentifiers(left, src, out)
		}
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		collectAssignTargets(node.Child(i), src, out)
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

	// Rails callback/validation DSL references methods by symbol literal
	// (`before_action :authenticate_user!`, `validate :check`). Record each as a
	// RelCalls edge on the enclosing class/module so callback-only methods are
	// not mis-reported as dead code.
	if rubyCallbackDSL[method] {
		if cur := w.cur(); cur != nil && cur.symFactIdx >= 0 {
			for _, name := range symbolArgs(args, w.src) {
				w.addCallToFact(cur.symFactIdx, name)
			}
		}
		return
	}

	// ActiveModel::Serializer attribute/association DSL: `attributes :a, :b`,
	// `attribute :c`, `has_one :user`, `has_many :posts`. Each declared name is
	// backed by a same-named method the serializer framework calls (when defined),
	// plus an optional `include_<name>?` predicate it calls to decide inclusion —
	// neither is an explicit Ruby call, so the backing methods look dead. Fold both
	// forms in as references on the enclosing serializer class. Gated on isSerializer
	// so the shared has_one/has_many/belongs_to names still reach the ActiveRecord
	// association handling below for models.
	if cur := w.cur(); cur != nil && cur.isSerializer && cur.symFactIdx >= 0 && rubySerializerDSL[method] {
		for _, name := range symbolArgs(args, w.src) {
			w.addCallToFact(cur.symFactIdx, name)
			w.addCallToFact(cur.symFactIdx, "include_"+name+"?")
		}
		return
	}

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
				// The raw association name as it is called in code (e.g. "posts" for
				// has_many :posts) — lets the perf analyzer flag lazy-loaded
				// association reads inside loops (N+1).
				"association": assoc,
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
