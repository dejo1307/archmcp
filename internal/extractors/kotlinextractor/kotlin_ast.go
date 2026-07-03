package kotlinextractor

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/enola-labs/enola/internal/facts"
	kotlin "github.com/tree-sitter-grammars/tree-sitter-kotlin/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractFileAST parses a Kotlin file using tree-sitter and emits architectural facts.
// Output is intended to be a superset of the legacy regex extractor's output: every
// declaration / import / Room-storage fact is preserved, and new RelInstantiates /
// RelInjects relations are attached to symbol facts when call sites or @Inject
// constructor parameters are observed.
func extractFileAST(src []byte, relFile string, isAndroid bool, sourceRoot, basePackage string) []facts.Fact {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(kotlin.Language())); err != nil {
		return nil
	}

	tree := parser.Parse(src, nil)
	defer tree.Close()

	root := tree.RootNode()
	dir := filepath.Dir(relFile)

	w := &astWalker{
		src:         src,
		relFile:     relFile,
		dir:         dir,
		isAndroid:   isAndroid,
		sourceRoot:  sourceRoot,
		basePackage: basePackage,
	}
	w.walkSourceFile(root)
	return w.out
}

type astWalker struct {
	src         []byte
	relFile     string
	dir         string
	isAndroid   bool
	sourceRoot  string
	basePackage string

	// out is the accumulating fact list.
	out []facts.Fact

	// ownerStack[len-1] points at the symbol fact currently being constructed.
	// New RelInstantiates / RelInjects edges discovered while walking that
	// symbol's body are appended to its Relations slice.
	ownerStack []*facts.Fact

	// importMap maps an imported simple name to its canonical symbol fact name
	// (e.g. "helper" → "src/util.helper"). Internal imports map to a resolvable
	// fact name; external imports map to "" (imported, but no local fact exists).
	// Used to resolve bare function calls to imported top-level functions.
	importMap map[string]string

	// typeStack holds the simple names of the enclosing class/object declarations,
	// so methods declared inside them are named "<dir>.<Type>.<method>" (matching
	// the Go/TypeScript extractors). methodStack is parallel to typeStack and holds
	// the set of method names declared directly in each enclosing type, used to
	// resolve same-class bare calls to "<dir>.<Type>.<method>".
	typeStack   []string
	methodStack []map[string]bool

	// Per-function complexity state, set up by handleFunctionDeclaration around
	// walkForCalls and saved/restored across the re-entrant nested-function walk.
	// metrics is nil outside a function body walk. loopDepth is the current loop
	// nesting depth; selfName/selfShort are the enclosing function's full and short
	// names (for direct-recursion detection).
	metrics   *kotlinBodyMetrics
	loopDepth int
	selfName  string
	selfShort string
}

// kotlinBodyMetrics accumulates per-function complexity signals during the single
// walkForCalls body traversal — mirrors the Go/Python/Ruby/Swift extractors.
type kotlinBodyMetrics struct {
	loopDepth   int             // max loop nesting depth
	loopCount   int             // number of loop constructs (syntactic + lambda iterators)
	decisions   int             // decision points (cyclomatic = 1 + decisions)
	callsInLoop []string        // distinct call targets invoked at loop depth >= 1
	inLoopSeen  map[string]bool // dedup set for callsInLoop
	recursive   bool            // body directly calls the enclosing function
}

// kotlinIterators are higher-order methods whose lambda runs once per element —
// i.e. a loop. A trailing lambda on a method NOT in this set (runBlocking, launch,
// withContext, let/also/apply/run/with) runs once and is not treated as a loop.
// Aggregate-or-iterate names (count/any/first…) are safe to include because a
// trailing lambda must be present before any of these counts as a loop.
var kotlinIterators = map[string]bool{
	"map": true, "mapNotNull": true, "mapIndexed": true, "flatMap": true,
	"forEach": true, "forEachIndexed": true, "onEach": true,
	"filter": true, "filterNot": true, "filterIndexed": true,
	"fold": true, "reduce": true, "sumOf": true, "count": true,
	"any": true, "all": true, "none": true, "find": true, "first": true,
	"firstOrNull": true, "last": true, "lastOrNull": true, "single": true,
	"sortedBy": true, "sortedByDescending": true, "groupBy": true,
	"associate": true, "associateBy": true, "associateWith": true,
	"partition": true, "maxByOrNull": true, "minByOrNull": true,
	"maxOfOrNull": true, "minOfOrNull": true, "takeWhile": true, "dropWhile": true,
}

// kotlinCheapMethods are obviously-cheap methods that are not I/O. No-arg-ish
// instance calls to these inside loops are not recorded in calls_in_loop, keeping
// it focused (the enterprise keyword gate is the real precision filter).
var kotlinCheapMethods = map[string]bool{
	"toString": true, "let": true, "also": true, "apply": true, "run": true,
	"with": true, "takeIf": true, "takeUnless": true, "size": true, "count": true,
	"isEmpty": true, "isNotEmpty": true, "length": true, "first": true, "last": true,
	"keys": true, "values": true, "trim": true, "trimEnd": true, "trimStart": true,
	"uppercase": true, "lowercase": true, "toInt": true, "toLong": true,
	"toDouble": true, "toFloat": true, "add": true, "append": true, "contains": true,
	"plus": true, "joinToString": true, "indexOf": true, "hashCode": true, "equals": true,
}

// recordCallMetrics notes a resolved call target against the current function's
// complexity metrics: flags direct recursion and records calls made inside loops.
func (w *astWalker) recordCallMetrics(target string) {
	if w.metrics == nil || target == "" {
		return
	}
	if target == w.selfShort || target == w.selfName || target == "this."+w.selfShort {
		w.metrics.recursive = true
	}
	w.recordInLoopCall(target)
}

// recordInLoopCall adds a target to calls_in_loop (deduped) when inside a loop,
// without the recursion check — used for raw instance-method names whose name must
// not be mistaken for self-recursion.
func (w *astWalker) recordInLoopCall(target string) {
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

func (w *astWalker) pushType(name string, methods map[string]bool) {
	w.typeStack = append(w.typeStack, name)
	w.methodStack = append(w.methodStack, methods)
}

func (w *astWalker) popType() {
	w.typeStack = w.typeStack[:len(w.typeStack)-1]
	w.methodStack = w.methodStack[:len(w.methodStack)-1]
}

// enclosingType returns the dotted path of enclosing type names (e.g. "Outer.Inner"),
// or "" when not inside a type.
func (w *astWalker) enclosingType() string {
	return strings.Join(w.typeStack, ".")
}

// currentMethods returns the set of method names declared in the innermost
// enclosing type, or nil when not inside a type.
func (w *astWalker) currentMethods() map[string]bool {
	if len(w.methodStack) == 0 {
		return nil
	}
	return w.methodStack[len(w.methodStack)-1]
}

// qualify prepends the enclosing type path to a declaration's name when inside a
// type, producing the canonical "<Type>.<name>" suffix; at top level it returns
// name unchanged.
func (w *astWalker) qualify(name string) string {
	if t := w.enclosingType(); t != "" {
		return t + "." + name
	}
	return name
}

func (w *astWalker) pushOwner(f *facts.Fact) { w.ownerStack = append(w.ownerStack, f) }
func (w *astWalker) popOwner()               { w.ownerStack = w.ownerStack[:len(w.ownerStack)-1] }
func (w *astWalker) currentOwner() *facts.Fact {
	if len(w.ownerStack) == 0 {
		return nil
	}
	return w.ownerStack[len(w.ownerStack)-1]
}

func (w *astWalker) walkSourceFile(root *sitter.Node) {
	for i := uint(0); i < uint(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Kind() {
		case "package_header":
			// no fact emitted; package is implied by `dir`
		case "import":
			w.handleImport(child)
		case "class_declaration":
			w.handleClassDeclaration(child)
		case "object_declaration":
			w.handleObjectDeclaration(child)
		case "function_declaration":
			w.handleFunctionDeclaration(child)
		case "property_declaration":
			w.handlePropertyDeclaration(child)
		case "type_alias":
			w.handleTypeAlias(child)
		}
	}
}

func (w *astWalker) handleImport(node *sitter.Node) {
	qid := findChildByKind(node, "qualified_identifier")
	if qid == nil {
		return
	}
	importPath := nodeText(qid, w.src)
	resolved, isExternal := resolveKotlinImport(importPath, w.sourceRoot, w.basePackage)
	importSource := "internal"
	if isExternal {
		importSource = "external"
	}
	w.out = append(w.out, facts.Fact{
		Kind: facts.KindDependency,
		Name: w.dir + " -> " + resolved,
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"language": "kotlin",
			"source":   importSource,
		},
		Relations: []facts.Relation{
			{Kind: facts.RelImports, Target: resolved},
		},
	})

	// Record the imported simple name so bare calls to imported top-level
	// functions can be resolved to a canonical fact name.
	simple := importPath
	if i := strings.LastIndex(importPath, "."); i >= 0 {
		simple = importPath[i+1:]
	}
	if simple == "" || simple == "*" {
		return // wildcard imports carry no simple name to resolve
	}
	if w.importMap == nil {
		w.importMap = make(map[string]string)
	}
	if isExternal {
		// Imported, but the target lives in an external dependency — no local
		// fact will match, so record the empty sentinel to suppress the edge.
		w.importMap[simple] = ""
	} else {
		w.importMap[simple] = symbolNameFromResolvedPath(resolved)
	}
}

// symbolNameFromResolvedPath converts a "/"-separated resolved import path (e.g.
// "src/util/helper") into the canonical symbol fact name "<dir>.<name>" (e.g.
// "src/util.helper") used by declaration facts.
func symbolNameFromResolvedPath(resolved string) string {
	if i := strings.LastIndex(resolved, "/"); i >= 0 {
		return resolved[:i] + "." + resolved[i+1:]
	}
	return resolved
}

func (w *astWalker) handleClassDeclaration(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)

	modifiers := findChildByKind(node, "modifiers")
	modifierText := ""
	annotations := []string{}
	if modifiers != nil {
		modifierText = nodeText(modifiers, w.src)
		annotations = annotationNames(modifiers, w.src)
	}

	// `class` vs `interface` — keyword sits as an anonymous child between modifiers and name.
	keyword := "class"
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Kind() == "interface" || (!c.IsNamed() && nodeText(c, w.src) == "interface") {
			keyword = "interface"
			break
		}
	}

	supertypes := supertypeNamesFromDelegationSpecifiers(node, w.src)

	symbolKind := facts.SymbolClass
	if keyword == "interface" {
		symbolKind = facts.SymbolInterface
	}
	exported := !privateOrInternalRe.MatchString(modifierText)

	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + w.qualify(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": symbolKind,
			"exported":    exported,
			"language":    "kotlin",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: w.dir},
		},
	}

	if strings.Contains(modifierText, "data") {
		f.Props["data_class"] = true
	}
	if strings.Contains(modifierText, "sealed") {
		f.Props["sealed"] = true
	}
	if strings.Contains(modifierText, "enum") {
		f.Props["enum"] = true
	}
	if strings.Contains(modifierText, "abstract") {
		f.Props["abstract"] = true
	}
	if strings.Contains(modifierText, "annotation") {
		f.Props["annotation_class"] = true
	}

	for _, st := range supertypes {
		f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelImplements, Target: st})
	}

	if w.isAndroid {
		// addAndroidProps uses the raw supertype clause text; reconstruct as a comma-joined
		// string so its supertypeMatches helper continues to work.
		addAndroidProps(&f, name, annotations, strings.Join(supertypes, ", "))
		if sf := detectRoomStorage(name, annotations, w.relFile, int(node.StartPosition().Row)+1, w.dir); sf != nil {
			w.out = append(w.out, *sf)
		}
	}

	// Push this class as the current owner so any constructor calls / @Inject params
	// found while walking its primary constructor + body attach back to it.
	w.out = append(w.out, f)
	owner := &w.out[len(w.out)-1]
	w.pushOwner(owner)

	// Enter the type scope: nested methods/classes are named "<dir>.<Type>.<...>",
	// and bare same-class calls resolve against this class's declared method names.
	body := findChildByKind(node, "class_body")
	if body == nil {
		body = findChildByKind(node, "enum_class_body")
	}
	w.pushType(name, collectMethodNames(body, w.src))

	// Primary constructor parameters → RelInjects when @Inject is on the class,
	// on the primary constructor itself (e.g., `class Foo @Inject constructor(...)`),
	// or directly on the parameter.
	classInjected := containsAnnotation(annotations, "Inject")
	if pc := findChildByKind(node, "primary_constructor"); pc != nil {
		if pcMods := findChildByKind(pc, "modifiers"); pcMods != nil {
			if containsAnnotation(annotationNames(pcMods, w.src), "Inject") {
				classInjected = true
			}
		}
		w.handleClassParameters(pc, classInjected)
	}

	// Walk class body for constructor calls (e.g., property initializers, init blocks,
	// method bodies). Calls discovered here attribute to the enclosing class symbol.
	if body != nil {
		w.walkForCalls(body)
	}

	// Also walk the delegation_specifiers — a `: Base(npeEntity(id))` supertype clause
	// can contain calls in its constructor arguments that would otherwise be missed.
	// The base type is a constructor_invocation (not a call_expression), so walking
	// here credits the argument calls without double-counting the base type, which is
	// already recorded as an implements edge.
	if ds := findChildByKind(node, "delegation_specifiers"); ds != nil {
		w.walkForCalls(ds)
	}

	w.popType()
	w.popOwner()
}

func (w *astWalker) handleObjectDeclaration(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)

	modifiers := findChildByKind(node, "modifiers")
	modifierText := ""
	annotations := []string{}
	if modifiers != nil {
		modifierText = nodeText(modifiers, w.src)
		annotations = annotationNames(modifiers, w.src)
	}

	supertypes := supertypeNamesFromDelegationSpecifiers(node, w.src)
	exported := !privateOrInternalRe.MatchString(modifierText)

	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + w.qualify(name),
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolClass,
			"exported":    exported,
			"language":    "kotlin",
			"object":      true,
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: w.dir},
		},
	}
	for _, st := range supertypes {
		f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelImplements, Target: st})
	}
	if w.isAndroid {
		addAndroidProps(&f, name, annotations, strings.Join(supertypes, ", "))
	}

	w.out = append(w.out, f)
	owner := &w.out[len(w.out)-1]
	w.pushOwner(owner)
	body := findChildByKind(node, "class_body")
	w.pushType(name, collectMethodNames(body, w.src))
	if body != nil {
		w.walkForCalls(body)
	}
	// Walk supertype constructor arguments (`object Foo : Base(bar())`), as in
	// handleClassDeclaration — the base type stays an implements edge, its argument
	// calls are credited here.
	if ds := findChildByKind(node, "delegation_specifiers"); ds != nil {
		w.walkForCalls(ds)
	}
	w.popType()
	w.popOwner()
}

func (w *astWalker) handleFunctionDeclaration(node *sitter.Node) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)

	modifiers := findChildByKind(node, "modifiers")
	modifierText := ""
	annotations := []string{}
	if modifiers != nil {
		modifierText = nodeText(modifiers, w.src)
		annotations = annotationNames(modifiers, w.src)
	}
	exported := !privateOrInternalRe.MatchString(modifierText)

	// A `fun` declared inside a class/object is a method, not a free function
	// (parity with the Go/Java extractors, which set SymbolMethod for members).
	// This keeps member functions out of the high-confidence orphan bucket, which
	// is reserved for plain functions whose incoming calls are reliably tracked as
	// edges — member functions are reached via receiver dispatch, overrides, and
	// reflection, which are not.
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
			"exported":    exported,
			"language":    "kotlin",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: w.dir},
		},
	}
	// When declared inside a class/object, record the enclosing type as the
	// receiver (parity with Go/TypeScript method facts).
	if len(w.typeStack) > 0 {
		f.Props["receiver"] = w.typeStack[len(w.typeStack)-1]
	}
	// An `override` is dispatched polymorphically — Android/framework lifecycle
	// callbacks (onCreate, onBind, …) and interface implementations are invoked
	// through the supertype, never by the override's own literal name, so it must
	// not be reported as an orphan.
	if strings.Contains(modifierText, "override") {
		f.Props["override"] = true
	}
	// Dagger/Hilt @Provides / @Binds methods are invoked reflectively by the DI
	// container, never by name.
	if containsAnnotation(annotations, "Provides") || containsAnnotation(annotations, "Binds") {
		f.Props["di_provider"] = true
	}
	if strings.Contains(modifierText, "suspend") {
		f.Props["suspend"] = true
	}
	if w.isAndroid && containsAnnotation(annotations, "Composable") {
		f.Props["android_component"] = "composable"
		f.Props["framework"] = "android"
	}

	w.out = append(w.out, f)
	ownerIdx := len(w.out) - 1
	w.pushOwner(&w.out[ownerIdx])
	// Set up per-function complexity tracking. walkForCalls is re-entrant (it
	// dispatches nested function_declarations back here), so save and restore the
	// outer state rather than clearing it. Props are written via the stable index
	// (the pointer may be invalidated if the body walk grows w.out).
	savedMetrics, savedDepth := w.metrics, w.loopDepth
	savedName, savedShort := w.selfName, w.selfShort
	w.metrics = &kotlinBodyMetrics{}
	w.loopDepth = 0
	w.selfName = f.Name
	w.selfShort = name
	if body := findChildByKind(node, "function_body"); body != nil {
		w.walkForCalls(body)
	}
	// Default parameter values (`fun f(x: T = helper())`) live outside the function
	// body, but their calls run when the function is invoked without that argument —
	// walk them under this function as owner so the referenced helper isn't
	// mis-reported as unused. (Class-constructor param defaults are already walked in
	// handleClassParameters.)
	if params := findChildByKind(node, "function_value_parameters"); params != nil {
		w.walkForCalls(params)
	}
	m := w.metrics
	props := w.out[ownerIdx].Props
	props["cyclomatic"] = 1 + m.decisions
	if m.loopDepth > 0 {
		props["loop_depth"] = m.loopDepth
	}
	if m.loopCount > 0 {
		props["loop_count"] = m.loopCount
	}
	if len(m.callsInLoop) > 0 {
		props["calls_in_loop"] = m.callsInLoop
	}
	if m.recursive {
		props["recursive_self"] = true
	}
	w.metrics, w.loopDepth = savedMetrics, savedDepth
	w.selfName, w.selfShort = savedName, savedShort
	w.popOwner()
}

func (w *astWalker) handlePropertyDeclaration(node *sitter.Node) {
	// Find variable_declaration child; first identifier inside it is the name.
	vd := findChildByKind(node, "variable_declaration")
	if vd == nil {
		return
	}
	nameNode := findFirstIdentifier(vd, w.src)
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)
	if name == "_" {
		return
	}

	modifiers := findChildByKind(node, "modifiers")
	modifierText := ""
	if modifiers != nil {
		modifierText = nodeText(modifiers, w.src)
	}
	exported := !privateOrInternalRe.MatchString(modifierText)

	// val => constant, var => variable.
	symbolKind := facts.SymbolVariable
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		if !c.IsNamed() && nodeText(c, w.src) == "val" {
			symbolKind = facts.SymbolConstant
			break
		}
	}

	f := facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + name,
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": symbolKind,
			"exported":    exported,
			"language":    "kotlin",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: w.dir},
		},
	}

	w.out = append(w.out, f)
	owner := &w.out[len(w.out)-1]
	w.pushOwner(owner)
	// Property initializer may contain a constructor call.
	w.walkForCalls(node)
	w.popOwner()
}

func (w *astWalker) handleTypeAlias(node *sitter.Node) {
	nameNode := node.ChildByFieldName("type")
	if nameNode == nil {
		return
	}
	name := nodeText(nameNode, w.src)

	modifiers := findChildByKind(node, "modifiers")
	modifierText := ""
	if modifiers != nil {
		modifierText = nodeText(modifiers, w.src)
	}
	exported := !privateOrInternalRe.MatchString(modifierText)

	w.out = append(w.out, facts.Fact{
		Kind: facts.KindSymbol,
		Name: w.dir + "." + name,
		File: w.relFile,
		Line: int(node.StartPosition().Row) + 1,
		Props: map[string]any{
			"symbol_kind": facts.SymbolType,
			"exported":    exported,
			"language":    "kotlin",
		},
		Relations: []facts.Relation{
			{Kind: facts.RelDeclares, Target: w.dir},
		},
	})
}

// handleClassParameters walks the primary_constructor's parameters and emits
// RelInjects edges from the enclosing class to each parameter's type when the
// class is annotated @Inject or the parameter itself carries @Inject.
//
// It also walks each parameter's default-value expression for constructor calls,
// so `private val x: Foo = Foo()` attributes a RelInstantiates Foo edge to the class.
func (w *astWalker) handleClassParameters(pc *sitter.Node, classInjected bool) {
	cps := findChildByKind(pc, "class_parameters")
	if cps == nil {
		return
	}
	owner := w.currentOwner()
	if owner == nil {
		return
	}
	for i := uint(0); i < uint(cps.ChildCount()); i++ {
		cp := cps.Child(i)
		if cp.Kind() != "class_parameter" {
			continue
		}
		paramInjected := classInjected
		if mods := findChildByKind(cp, "modifiers"); mods != nil {
			if containsAnnotation(annotationNames(mods, w.src), "Inject") {
				paramInjected = true
			}
		}
		// Resolve the parameter type. `type` is inlined (supertype rule), so
		// look for one of its concrete child kinds.
		typeName := lastTypeIdentifier(firstTypeChild(cp), w.src)
		if paramInjected && typeName != "" {
			owner.Relations = append(owner.Relations, facts.Relation{
				Kind:   facts.RelInjects,
				Target: typeName,
			})
		}
		// Walk default expression (anything after `=`) for constructor calls.
		w.walkForCalls(cp)
	}
}

// walkForCalls recursively scans the subtree for call_expression and
// constructor_invocation nodes, emitting RelInstantiates on the current owner
// when the callee identifier looks like a type name (starts with uppercase).
func (w *astWalker) walkForCalls(node *sitter.Node) {
	if node == nil {
		return
	}
	kind := node.Kind()

	// A lambda is a deferred scope: its body runs when the lambda is invoked, NOT
	// per-iteration of the enclosing loops — so reset the loop depth for its
	// subtree (e.g. a click handler defined inside a `forEach { … }` must not be
	// counted as a per-iteration call). The iterator's OWN lambda is handled in the
	// call_expression branch (its body walks at +1).
	if w.metrics != nil && kind == "lambda_literal" {
		saved := w.loopDepth
		w.loopDepth = 0
		for i := uint(0); i < uint(node.ChildCount()); i++ {
			w.walkForCalls(node.Child(i))
		}
		w.loopDepth = saved
		return
	}

	// Complexity metrics: count decision points so the single body walk doubles as
	// the cyclomatic pass. (Statement node kinds don't collide with the anonymous
	// keyword tokens, so no IsNamed guard is needed.)
	if w.metrics != nil {
		switch kind {
		case "if_expression", "when_entry", "catch_block":
			w.metrics.decisions++
		case "binary_expression":
			if kotlinBooleanOp(node) {
				w.metrics.decisions++
			}
		}
	}

	// Syntactic loops: everything in the body runs per iteration.
	switch kind {
	case "for_statement", "while_statement", "do_while_statement":
		if w.metrics != nil {
			w.metrics.loopCount++
			w.metrics.decisions++
			if w.loopDepth+1 > w.metrics.loopDepth {
				w.metrics.loopDepth = w.loopDepth + 1
			}
		}
		w.loopDepth++
		for i := uint(0); i < uint(node.ChildCount()); i++ {
			w.walkChild(node.Child(i))
		}
		w.loopDepth--
		return
	}

	if kind == "call_expression" {
		var iterLambda *sitter.Node
		// First named child is the callee expression.
		if callee := firstNamedChild(node); callee != nil {
			if name, isNav := calleeName(callee, w.src); name != "" {
				if owner := w.currentOwner(); owner != nil {
					switch {
					case isCapitalized(name):
						// Kotlin convention: a capitalized callee is a constructor.
						owner.Relations = append(owner.Relations, facts.Relation{
							Kind:   facts.RelInstantiates,
							Target: name,
						})
					case !isNav:
						// Bare lowercase call: a same-class method, a same-package
						// function, or an imported top-level function.
						if target := w.resolveCall(name); target != "" {
							owner.Relations = append(owner.Relations, facts.Relation{
								Kind:   facts.RelCalls,
								Target: target,
							})
							w.recordCallMetrics(target)
						}
					case navReceiverIsThis(callee, w.src):
						// `this.method()` resolves to a sibling method of the
						// enclosing class. Other navigation calls (method calls on a
						// receiver of unknown type) are left unresolved.
						if methods := w.currentMethods(); methods[name] {
							t := w.dir + "." + w.enclosingType() + "." + name
							owner.Relations = append(owner.Relations, facts.Relation{
								Kind:   facts.RelCalls,
								Target: t,
							})
							w.recordCallMetrics(t)
						}
					case w.loopDepth > 0 && isNav && !kotlinCheapMethods[name]:
						// Method call on a non-this receiver inside a loop (dao.insert,
						// repo.getAll). No graph edge today, but its name feeds the perf
						// metric so the enterprise analyzer can flag per-iteration I/O.
						tgt := name
						if r := firstNamedChild(callee); r != nil {
							if recv := nodeText(r, w.src); recv != "" {
								tgt = recv + "." + name
							}
						}
						w.recordInLoopCall(tgt)
					}
				}
				// An iterator method with a trailing lambda (items.map { … }) is a
				// loop: its lambda body runs per element, but the receiver/arguments
				// run once.
				if w.metrics != nil && kotlinIterators[name] {
					iterLambda = findChildByKind(node, "annotated_lambda")
				}
			}
		}
		if iterLambda != nil {
			w.metrics.loopCount++
			w.metrics.decisions++
			if w.loopDepth+1 > w.metrics.loopDepth {
				w.metrics.loopDepth = w.loopDepth + 1
			}
			for i := uint(0); i < uint(node.ChildCount()); i++ {
				if c := node.Child(i); byteContains(c, iterLambda) {
					w.walkLambdaSubtree(c, iterLambda)
				} else {
					w.walkChild(c)
				}
			}
			return
		}
	}

	// A navigation expression (`recv.member` — a method call `recv.method()` or a
	// property/field access `recv.uniqueId`) references its trailing member by short
	// name. The receiver's static type is unknown without a type system, so we cannot
	// resolve it to a canonical fact name — but the orphan detector matches references
	// by short name, so emitting the bare member name is enough to mark the target
	// member used. This covers the dominant Kotlin/Android call form (viewModel.load(),
	// repo.getUser()) and property access (slot.uniqueId), none of which produced a
	// usage edge before, which is why live members were reported as orphans.
	if kind == "navigation_expression" {
		if owner := w.currentOwner(); owner != nil {
			if member, _ := calleeName(node, w.src); member != "" {
				owner.Relations = append(owner.Relations, facts.Relation{
					Kind:   facts.RelCalls,
					Target: member,
				})
			}
		}
	}

	// A callable reference (`::foo`, `Type::foo`) uses the referenced function by
	// short name without calling it directly — capture it so a function referenced
	// only as a method reference (e.g. `onClick = ::doNothing`, `.map(::helper)`) is
	// not mis-reported as an orphan. The trailing identifier is the callable name.
	if kind == "callable_reference" {
		if owner := w.currentOwner(); owner != nil {
			var last *sitter.Node
			for i := uint(0); i < uint(node.ChildCount()); i++ {
				if c := node.Child(i); c.IsNamed() {
					last = c
				}
			}
			if last != nil {
				if name := nodeText(last, w.src); name != "" {
					owner.Relations = append(owner.Relations, facts.Relation{
						Kind:   facts.RelCalls,
						Target: name,
					})
				}
			}
		}
	}

	for i := uint(0); i < uint(node.ChildCount()); i++ {
		w.walkChild(node.Child(i))
	}
}

// walkChild recurses into a child, dispatching nested declarations to their own
// handlers (so their calls are attributed to their own owner, not the enclosing
// function).
func (w *astWalker) walkChild(c *sitter.Node) {
	switch c.Kind() {
	case "class_declaration":
		w.handleClassDeclaration(c)
	case "object_declaration":
		w.handleObjectDeclaration(c)
	case "function_declaration":
		w.handleFunctionDeclaration(c)
	default:
		w.walkForCalls(c)
	}
}

// kotlinBooleanOp reports whether a binary_expression's operator is a logical
// connective or the Elvis operator — each adds a path for cyclomatic complexity.
func kotlinBooleanOp(node *sitter.Node) bool {
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		switch node.Child(i).Kind() {
		case "&&", "||", "?:":
			return true
		}
	}
	return false
}

func byteContains(outer, inner *sitter.Node) bool {
	return inner.StartByte() >= outer.StartByte() && inner.EndByte() <= outer.EndByte()
}

// walkLambdaSubtree descends toward an iterator's trailing lambda, bumping the loop
// depth exactly at the lambda (its body is per-iteration) while walking everything
// else (the receiver, sibling arguments) at the current depth.
func (w *astWalker) walkLambdaSubtree(node, lambda *sitter.Node) {
	if node == nil {
		return
	}
	if node.StartByte() == lambda.StartByte() && node.EndByte() == lambda.EndByte() {
		// The iterator invokes this lambda per element: walk its BODY at +1. Descend
		// to the inner lambda_literal and walk ITS children directly, rather than
		// walkForCalls(node), which would treat the lambda as a deferred scope and
		// reset the depth.
		body := node
		if lit := findChildByKind(node, "lambda_literal"); lit != nil {
			body = lit
		}
		w.loopDepth++
		for i := uint(0); i < uint(body.ChildCount()); i++ {
			w.walkChild(body.Child(i))
		}
		w.loopDepth--
		return
	}
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		if c := node.Child(i); byteContains(c, lambda) {
			w.walkLambdaSubtree(c, lambda)
		} else {
			w.walkChild(c)
		}
	}
}

// calleeName inspects a call_expression's callee and returns its simple name
// (the trailing identifier for navigation expressions) along with whether the
// call was made through a navigation expression (e.g. `a.b.foo()`). Callers use
// the name's capitalization to decide between a constructor (RelInstantiates) and
// a function/method call (RelCalls), and isNav to decide whether the call target
// is resolvable.
func calleeName(callee *sitter.Node, src []byte) (string, bool) {
	switch callee.Kind() {
	case "simple_identifier", "identifier":
		return nodeText(callee, src), false
	case "navigation_expression":
		// Last named child is the trailing identifier.
		var last *sitter.Node
		for i := uint(0); i < uint(callee.ChildCount()); i++ {
			c := callee.Child(i)
			if c.IsNamed() {
				last = c
			}
		}
		if last != nil {
			return nodeText(last, src), true
		}
	}
	return "", false
}

// collectMethodNames returns the set of method names declared directly in a
// class/object body. It is used to resolve bare same-class calls to qualified
// "<dir>.<Type>.<method>" fact names.
func collectMethodNames(body *sitter.Node, src []byte) map[string]bool {
	methods := make(map[string]bool)
	if body == nil {
		return methods
	}
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		c := body.Child(i)
		if c.Kind() != "function_declaration" {
			continue
		}
		if nameNode := c.ChildByFieldName("name"); nameNode != nil {
			methods[nodeText(nameNode, src)] = true
		}
	}
	return methods
}

// kotlinBuiltins are Kotlin standard-library functions that are auto-imported
// (kotlin.*, kotlin.collections.*, kotlin.io.*, etc.) and so appear as bare calls
// without an explicit import statement. They are not project symbols, so resolving
// them would produce dangling phantom call edges (the Kotlin analog of goBuiltins).
var kotlinBuiltins = map[string]bool{
	// Scope & control functions.
	"let": true, "run": true, "with": true, "apply": true, "also": true,
	"takeIf": true, "takeUnless": true, "repeat": true, "synchronized": true,
	"lazy": true, "lazyOf": true,
	// Preconditions & errors.
	"require": true, "requireNotNull": true, "check": true, "checkNotNull": true,
	"error": true, "TODO": true, "assert": true, "runCatching": true,
	// IO.
	"print": true, "println": true, "readLine": true, "readln": true,
	"readlnOrNull": true,
	// Collection / array / sequence / string builders.
	"listOf": true, "listOfNotNull": true, "mutableListOf": true, "arrayListOf": true,
	"setOf": true, "setOfNotNull": true, "mutableSetOf": true, "hashSetOf": true,
	"linkedSetOf": true, "sortedSetOf": true, "mapOf": true, "mutableMapOf": true,
	"hashMapOf": true, "linkedMapOf": true, "sortedMapOf": true, "emptyList": true,
	"emptySet": true, "emptyMap": true, "emptyArray": true, "emptySequence": true,
	"arrayOf": true, "arrayOfNulls": true, "booleanArrayOf": true, "byteArrayOf": true,
	"charArrayOf": true, "doubleArrayOf": true, "floatArrayOf": true, "intArrayOf": true,
	"longArrayOf": true, "shortArrayOf": true, "sequenceOf": true, "buildList": true,
	"buildMap": true, "buildSet": true, "buildString": true,
	// Numeric helpers.
	"maxOf": true, "minOf": true,
}

// resolveCall maps a bare call name to a canonical symbol fact name, in order of
// preference:
//  1. a sibling method of the enclosing class → "<dir>.<Type>.<name>"
//  2. an imported top-level function → its mapped target ("" when external)
//  3. a Kotlin stdlib/scope function (auto-imported) → "" (no project symbol)
//  4. otherwise a same-package top-level function → "<dir>.<name>"
func (w *astWalker) resolveCall(name string) string {
	if methods := w.currentMethods(); methods[name] {
		return w.dir + "." + w.enclosingType() + "." + name
	}
	if target, ok := w.importMap[name]; ok {
		return target
	}
	if kotlinBuiltins[name] {
		return ""
	}
	return w.dir + "." + name
}

// navReceiverIsThis reports whether a navigation_expression callee's receiver is
// the bare `this` keyword (e.g. `this.foo()`), so the call can be resolved against
// the enclosing class's methods.
func navReceiverIsThis(callee *sitter.Node, src []byte) bool {
	if callee.Kind() != "navigation_expression" {
		return false
	}
	first := firstNamedChild(callee)
	return first != nil && nodeText(first, src) == "this"
}

func isCapitalized(s string) bool {
	if s == "" {
		return false
	}
	r := []rune(s)[0]
	return unicode.IsUpper(r)
}

// supertypeNamesFromDelegationSpecifiers walks the delegation_specifiers child
// of a class/object declaration and returns the simple type name of each.
//
// Grammar:
//
//	delegation_specifier: repeat(annotation) (constructor_invocation | explicit_delegation | type)
//	constructor_invocation: type value_arguments
//	type: user_type | nullable_type | function_type | non_nullable_type | parenthesized_type | 'dynamic'
//
// Because `type` is declared as a tree-sitter supertype rule, it gets inlined:
// instead of a literal `type` node we see one of its choices (most commonly
// `user_type`) directly under the parent.
func supertypeNamesFromDelegationSpecifiers(decl *sitter.Node, src []byte) []string {
	ds := findChildByKind(decl, "delegation_specifiers")
	if ds == nil {
		return nil
	}
	var names []string
	for i := uint(0); i < uint(ds.ChildCount()); i++ {
		spec := ds.Child(i)
		if spec.Kind() != "delegation_specifier" {
			continue
		}
		var typeNode *sitter.Node
		for j := uint(0); j < uint(spec.ChildCount()); j++ {
			c := spec.Child(j)
			switch c.Kind() {
			case "constructor_invocation", "explicit_delegation":
				// Both contain a `type` (inlined as user_type/nullable_type/etc.)
				typeNode = firstTypeChild(c)
			case "user_type", "nullable_type", "non_nullable_type", "function_type", "parenthesized_type":
				typeNode = c
			}
			if typeNode != nil {
				break
			}
		}
		if name := lastTypeIdentifier(typeNode, src); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// firstTypeChild finds the first child of `parent` that is one of the inlined
// `type` choices.
func firstTypeChild(parent *sitter.Node) *sitter.Node {
	if parent == nil {
		return nil
	}
	for i := uint(0); i < uint(parent.ChildCount()); i++ {
		c := parent.Child(i)
		switch c.Kind() {
		case "user_type", "nullable_type", "non_nullable_type", "function_type", "parenthesized_type":
			return c
		}
	}
	return nil
}

// lastTypeIdentifier returns the simple (rightmost) identifier of a type node,
// stripping generic arguments. For `com.example.Foo<Bar>` it returns "Foo".
// Accepts user_type / nullable_type / non_nullable_type / etc. (the inlined
// children of the grammar's `type` supertype).
func lastTypeIdentifier(typeNode *sitter.Node, src []byte) string {
	if typeNode == nil {
		return ""
	}
	ut := typeNode
	// Unwrap nullable_type to its inner user_type.
	if ut.Kind() == "nullable_type" {
		if inner := firstTypeChild(ut); inner != nil {
			ut = inner
		}
	}
	if ut.Kind() != "user_type" {
		// Fall back to text parsing for function/parenthesized/non_nullable types.
		t := nodeText(ut, src)
		if i := strings.IndexAny(t, "<?"); i >= 0 {
			t = t[:i]
		}
		if i := strings.LastIndex(t, "."); i >= 0 {
			t = t[i+1:]
		}
		return strings.TrimSpace(t)
	}
	// user_type is `sep1(_simple_user_type, '.')` — its named children are
	// hidden simple_user_type nodes (each containing an identifier and optional
	// type_arguments). Take the last one.
	var last *sitter.Node
	for i := uint(0); i < uint(ut.ChildCount()); i++ {
		c := ut.Child(i)
		if c.IsNamed() {
			last = c
		}
	}
	if last == nil {
		// User type with no named children: take its text.
		t := nodeText(ut, src)
		if i := strings.IndexAny(t, "<?"); i >= 0 {
			t = t[:i]
		}
		if i := strings.LastIndex(t, "."); i >= 0 {
			t = t[i+1:]
		}
		return strings.TrimSpace(t)
	}
	if id := findFirstIdentifier(last, src); id != nil {
		return nodeText(id, src)
	}
	t := nodeText(last, src)
	if i := strings.IndexAny(t, "<?"); i >= 0 {
		t = t[:i]
	}
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSpace(t)
}

// annotationNames extracts the annotation simple-names from a `modifiers` node.
// For `@HiltViewModel @Inject` it returns ["HiltViewModel", "Inject"]. Use-site
// targets and arguments are ignored.
//
// Grammar: annotation -> '@' optional(use_site_target) _unescaped_annotation;
// _unescaped_annotation is a hidden choice of constructor_invocation | type, so
// the annotation's named children are the type itself (inlined as user_type,
// nullable_type, etc.) or a constructor_invocation that wraps such a type.
func annotationNames(modifiers *sitter.Node, src []byte) []string {
	if modifiers == nil {
		return nil
	}
	var out []string
	for i := uint(0); i < uint(modifiers.ChildCount()); i++ {
		c := modifiers.Child(i)
		if c.Kind() != "annotation" {
			continue
		}
		var nameNode *sitter.Node
		for j := uint(0); j < uint(c.ChildCount()); j++ {
			cc := c.Child(j)
			switch cc.Kind() {
			case "constructor_invocation":
				nameNode = firstTypeChild(cc)
			case "user_type", "nullable_type", "non_nullable_type":
				nameNode = cc
			}
			if nameNode != nil {
				break
			}
		}
		if nameNode != nil {
			if n := lastTypeIdentifier(nameNode, src); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// --- tree-sitter helpers ---

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

// findFirstIdentifier returns the first descendant identifier-ish node. It
// prefers a direct `simple_identifier` or `identifier` child; otherwise drills
// into the first named child recursively.
func findFirstIdentifier(node *sitter.Node, src []byte) *sitter.Node {
	if node == nil {
		return nil
	}
	if node.Kind() == "identifier" || node.Kind() == "simple_identifier" {
		return node
	}
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		if !c.IsNamed() {
			continue
		}
		if c.Kind() == "identifier" || c.Kind() == "simple_identifier" {
			return c
		}
	}
	for i := uint(0); i < uint(node.ChildCount()); i++ {
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
