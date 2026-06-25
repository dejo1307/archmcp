package pythonextractor

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/enola-labs/enola/internal/facts"
	sitter "github.com/tree-sitter/go-tree-sitter"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// extractFileAST parses a Python file with tree-sitter and emits architectural
// facts. It is a superset of extractFile: every symbol / import / route / storage
// fact is preserved, and RelCalls / RelInstantiates edges are added when call
// sites are observed inside function bodies.
func extractFileAST(src []byte, relFile string, isDjango bool, idx *pySymbolIndex) []facts.Fact {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(python.Language())); err != nil {
		return nil
	}

	tree := parser.Parse(src, nil)
	defer tree.Close()

	module := strings.TrimSuffix(relFile, ".py")
	dir := filepath.Dir(relFile)

	w := &pyWalker{
		src:      src,
		relFile:  relFile,
		module:   module,
		dir:      dir,
		isDjango: isDjango,
		idx:      idx,
	}
	w.walkModule(tree.RootNode())
	return w.out
}

type pyWalker struct {
	src      []byte
	relFile  string
	module   string
	dir      string
	isDjango bool

	out []facts.Fact

	// typeStack holds enclosing class names so methods get qualified names.
	typeStack []string

	// ownerStack: top element is the index into w.out of the fact that receives
	// RelCalls / RelInstantiates discovered while walking its body. Indices are
	// used instead of pointers because appending to w.out can reallocate the
	// backing array, invalidating any previously captured pointer.
	ownerStack []int

	// importMap maps a local name to its canonical fact target (empty = external).
	importMap map[string]string

	// methodSets[i] is the set of methods declared directly in typeStack[i],
	// used to resolve bare same-class calls.
	methodSets []map[string]bool

	// idx is the global symbol index, nil when called from tests that do not
	// need cross-file resolution. All lookups must nil-check.
	idx *pySymbolIndex

	// localTypes maps a variable name in the current function scope to its
	// canonical qualified type. Reset at the entry of every handleFunction call.
	localTypes map[string]string

	// Per-function complexity state, set up by handleFunction around walkForCalls.
	// metrics is nil outside a function body walk. loopDepth is the current loop
	// nesting depth; selfName is the enclosing function's qualified name (for
	// direct-recursion detection).
	metrics   *pyBodyMetrics
	loopDepth int
	selfName  string
}

// pyBodyMetrics accumulates per-function complexity signals during the single
// walkForCalls body traversal — mirrors the Go extractor's bodyMetrics.
type pyBodyMetrics struct {
	loopDepth   int             // max loop nesting depth
	loopCount   int             // number of loop/comprehension constructs
	decisions   int             // decision points (cyclomatic = 1 + decisions)
	callsInLoop []string        // distinct call targets invoked at loop depth >= 1
	inLoopSeen  map[string]bool // dedup set for callsInLoop
	recursive   bool            // body directly calls the enclosing function
}

// recordCallMetrics notes a resolved call target against the current function's
// complexity metrics: flags direct recursion and records calls made inside loops.
func (w *pyWalker) recordCallMetrics(target string) {
	if w.metrics == nil || target == "" {
		return
	}
	if target == w.selfName {
		w.metrics.recursive = true
	}
	if w.loopDepth > 0 {
		if w.metrics.inLoopSeen == nil {
			w.metrics.inLoopSeen = make(map[string]bool)
		}
		if !w.metrics.inLoopSeen[target] {
			w.metrics.inLoopSeen[target] = true
			w.metrics.callsInLoop = append(w.metrics.callsInLoop, target)
		}
	}
}

func (w *pyWalker) pushOwner(idx int) { w.ownerStack = append(w.ownerStack, idx) }
func (w *pyWalker) popOwner()         { w.ownerStack = w.ownerStack[:len(w.ownerStack)-1] }
func (w *pyWalker) currentOwner() *facts.Fact {
	if len(w.ownerStack) == 0 {
		return nil
	}
	return &w.out[w.ownerStack[len(w.ownerStack)-1]]
}

func (w *pyWalker) enclosingType() string { return strings.Join(w.typeStack, ".") }

func (w *pyWalker) qualify(name string) string {
	if t := w.enclosingType(); t != "" {
		return t + "." + name
	}
	return name
}

func (w *pyWalker) pushType(name string, methods map[string]bool) {
	w.typeStack = append(w.typeStack, name)
	w.methodSets = append(w.methodSets, methods)
}

func (w *pyWalker) popType() {
	w.typeStack = w.typeStack[:len(w.typeStack)-1]
	w.methodSets = w.methodSets[:len(w.methodSets)-1]
}

func (w *pyWalker) currentMethods() map[string]bool {
	if len(w.methodSets) == 0 {
		return nil
	}
	return w.methodSets[len(w.methodSets)-1]
}

// walkModule iterates the top-level statements of a module node.
func (w *pyWalker) walkModule(root *sitter.Node) {
	for i := uint(0); i < uint(root.ChildCount()); i++ {
		w.walkStatement(root.Child(i))
	}
}

func (w *pyWalker) walkStatement(node *sitter.Node) {
	if node == nil {
		return
	}
	switch node.Kind() {
	case "import_statement":
		w.handleImport(node)
	case "import_from_statement":
		w.handleFromImport(node)
	case "class_definition":
		w.handleClass(node, nil)
	case "function_definition":
		w.handleFunction(node, nil)
	case "decorated_definition":
		w.handleDecoratedDefinition(node)
	case "expression_statement":
		// __tablename__ = "foo" (SQLAlchemy) lives here at class body level.
		// urlpatterns = [...] (Django) lives at module level.
		w.handleExprStatement(node)
	case "assignment":
		// tree-sitter may parse assignments as "assignment" nodes at module level.
		w.handleAssignment(node)
	case "block":
		for i := uint(0); i < uint(node.ChildCount()); i++ {
			w.walkStatement(node.Child(i))
		}
	}
}

// handleImport handles `import foo.bar` — emits KindDependency + RelImports.
func (w *pyWalker) handleImport(node *sitter.Node) {
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Kind() == "dotted_name" || c.Kind() == "aliased_import" {
			var name, alias string
			if c.Kind() == "aliased_import" {
				nameNode := c.ChildByFieldName("name")
				aliasNode := c.ChildByFieldName("alias")
				if nameNode == nil {
					continue
				}
				name = pyText(c.ChildByFieldName("name"), w.src)
				if aliasNode != nil {
					alias = pyText(aliasNode, w.src)
				}
			} else {
				name = pyText(c, w.src)
			}
			target := w.module + " -> " + name
			w.out = append(w.out, facts.Fact{
				Kind:  facts.KindDependency,
				Name:  target,
				File:  w.relFile,
				Line:  int(node.StartPosition().Row) + 1,
				Props: map[string]any{"language": "python"},
				Relations: []facts.Relation{
					{Kind: facts.RelImports, Target: name},
				},
			})
			local := alias
			if local == "" {
				if dot := strings.LastIndex(name, "."); dot >= 0 {
					local = name[dot+1:]
				} else {
					local = name
				}
			}
			w.setImport(local, "")
		}
	}
}

// handleFromImport handles `from foo.bar import Baz, Qux`.
func (w *pyWalker) handleFromImport(node *sitter.Node) {
	moduleNode := node.ChildByFieldName("module_name")
	if moduleNode == nil {
		return
	}
	moduleName := pyText(moduleNode, w.src)

	// Determine if this is an intra-project import (relative or same-tree dotted).
	isRelative := strings.HasPrefix(moduleName, ".") ||
		strings.HasPrefix(pyText(node, w.src), "from .")

	target := w.module + " -> " + moduleName
	depProps := map[string]any{"language": "python", "from": true}
	w.out = append(w.out, facts.Fact{
		Kind:  facts.KindDependency,
		Name:  target,
		File:  w.relFile,
		Line:  int(node.StartPosition().Row) + 1,
		Props: depProps,
		Relations: []facts.Relation{
			{Kind: facts.RelImports, Target: moduleName},
		},
	})

	// For __init__.py, record the imported short names so the dead-code tool can
	// treat them as re-exported (part of the package's public surface) and not
	// flag them as orphans. Kept only here to avoid bloating every from-import.
	isInit := w.relFile == "__init__.py" || strings.HasSuffix(w.relFile, "/__init__.py")
	var reexported []string

	// Map each imported name to a resolvable target or "" (external).
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Kind() != "dotted_name" && c.Kind() != "identifier" && c.Kind() != "aliased_import" {
			continue
		}
		var localName, importedName string
		if c.Kind() == "aliased_import" {
			n := c.ChildByFieldName("name")
			a := c.ChildByFieldName("alias")
			if n == nil {
				continue
			}
			importedName = pyText(n, w.src)
			if a != nil {
				localName = pyText(a, w.src)
			} else {
				localName = importedName
			}
		} else {
			importedName = pyText(c, w.src)
			localName = importedName
		}

		if isInit && importedName != "" && importedName != "*" {
			reexported = append(reexported, importedName)
		}

		if isRelative {
			// Relative import → resolve to a local module path.
			base := moduleName
			if strings.HasPrefix(base, ".") {
				base = w.dir + "/" + strings.TrimLeft(base, ".")
			}
			w.setImport(localName, base+"."+importedName)
		} else {
			// External or ambiguous — suppress call edges to this name.
			w.setImport(localName, "")
		}
	}

	if len(reexported) > 0 {
		depProps["reexports"] = reexported
	}
}

func (w *pyWalker) setImport(local, target string) {
	if local == "" || local == "*" {
		return
	}
	if w.importMap == nil {
		w.importMap = make(map[string]string)
	}
	w.importMap[local] = target
}

// handleDecoratedDefinition unwraps `@decorator\ndef/class ...` nodes.
func (w *pyWalker) handleDecoratedDefinition(node *sitter.Node) {
	var decorators []string
	var pendingApiViewMethods []string
	// pendingRouteIndices holds w.out indices of route facts emitted from
	// decorators before we see the handler name. Indices are used (not pointers)
	// because subsequent appends to w.out may reallocate its backing array.
	var pendingRouteIndices []int

	for i := uint(0); i < uint(node.ChildCount()); i++ {
		c := node.Child(i)
		switch c.Kind() {
		case "decorator":
			text := pyText(c, w.src)
			// FastAPI / Starlette route decorator.
			if m := routeDecoratorRe.FindStringSubmatch(text); m != nil {
				method := strings.ToUpper(m[2])
				path := m[3]
				w.out = append(w.out, facts.Fact{
					Kind: facts.KindRoute,
					// Name is the bare path (like every other extractor) so the
					// cross-repo linker, which treats route Name as the path, can match
					// it. Multiple methods on one path produce same-Name facts
					// disambiguated by the method prop — the linker indexes by (path, method).
					Name: path,
					File: w.relFile,
					Line: int(c.StartPosition().Row) + 1,
					Props: map[string]any{
						"role":      "server",
						"method":    method,
						"path":      path,
						"framework": "fastapi",
						"language":  "python",
					},
				})
				pendingRouteIndices = append(pendingRouteIndices, len(w.out)-1)
				continue
			}
			// DRF @api_view(['GET','POST']).
			if m := apiViewRe.FindStringSubmatch(text); m != nil {
				pendingApiViewMethods = append(pendingApiViewMethods, httpMethodWordRe.FindAllString(m[1], -1)...)
				continue
			}
			// Generic decorator name capture.
			if m := decoratorRe.FindStringSubmatch(text); m != nil {
				decorators = append(decorators, m[1])
			}

		case "function_definition":
			// @overload stubs are type-checker-only annotations with no runtime
			// body — skip them to avoid duplicate symbol facts.
			if hasDecorator(decorators, "overload") {
				continue
			}
			w.handleFunction(c, decorators)
			handlerName := w.module + "." + w.qualify(pyFuncName(c, w.src))
			// Back-fill handler into pending FastAPI route facts.
			for _, idx := range pendingRouteIndices {
				w.out[idx].Props["handler"] = handlerName
			}
			// @api_view routes — emit after we know the handler name.
			if len(pendingApiViewMethods) > 0 {
				for _, meth := range pendingApiViewMethods {
					w.out = append(w.out, facts.Fact{
						Kind: facts.KindRoute,
						Name: meth + " (view) " + handlerName,
						File: w.relFile,
						Line: int(c.StartPosition().Row) + 1,
						Props: map[string]any{
							"role":      "server",
							"method":    meth,
							"framework": "django",
							"handler":   handlerName,
							"language":  "python",
						},
					})
				}
			}

		case "class_definition":
			w.handleClass(c, decorators)
		}
	}
}

// handleClass emits a KindSymbol fact for a class and walks its body.
func (w *pyWalker) handleClass(node *sitter.Node, decorators []string) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := pyText(nameNode, w.src)
	qualName := w.module + "." + w.qualify(name)

	props := map[string]any{
		"symbol_kind": facts.SymbolClass,
		"language":    "python",
		// Python has no access keyword; the leading-underscore convention marks
		// a name as private/internal. Lets the dead-code visibility filter work.
		"exported": !strings.HasPrefix(name, "_"),
	}
	rels := []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}}

	// Superclasses.
	var bases []string
	abstract := false
	if args := node.ChildByFieldName("superclasses"); args != nil {
		for i := uint(0); i < uint(args.ChildCount()); i++ {
			c := args.Child(i)
			switch c.Kind() {
			case "identifier":
				base := pyText(c, w.src)
				bases = append(bases, base)
				rels = append(rels, facts.Relation{Kind: facts.RelImplements, Target: base})
			case "attribute":
				base := pyText(c, w.src)
				bases = append(bases, base)
				rels = append(rels, facts.Relation{Kind: facts.RelImplements, Target: base})
			case "subscript":
				// Generic base: CRUDBase[ModelType, IdType] — strip the type params.
				valueNode := c.ChildByFieldName("value")
				if valueNode != nil {
					base := pyText(valueNode, w.src)
					bases = append(bases, base)
					rels = append(rels, facts.Relation{Kind: facts.RelImplements, Target: base})
				}
			case "keyword_argument":
				// metaclass=ABCMeta makes the class abstract.
				nameNode := c.ChildByFieldName("name")
				valueNode := c.ChildByFieldName("value")
				if nameNode != nil && valueNode != nil &&
					pyText(nameNode, w.src) == "metaclass" &&
					lastComponent(pyText(valueNode, w.src)) == "ABCMeta" {
					abstract = true
				}
			}
		}
	}
	// A class is abstract if it inherits ABC/ABCMeta/Protocol, uses metaclass=ABCMeta,
	// or declares any @abstractmethod. Recorded so package-metrics abstractness (A)
	// is meaningful for Python (which has no interface keyword).
	for _, base := range bases {
		if pyAbstractBases[lastComponent(base)] {
			abstract = true
			break
		}
	}
	if !abstract && bodyHasAbstractMethod(node.ChildByFieldName("body"), w.src) {
		abstract = true
	}
	if abstract {
		props["abstract"] = true
	}

	for _, dec := range decorators {
		applyDecoratorProps(props, dec)
	}

	// Django classification.
	if w.isDjango {
		for _, base := range bases {
			last := lastComponent(base)
			if djangoModelBases[last] {
				props["framework"] = "django"
				tableName := camelToSnake(name)
				w.out = append(w.out, facts.Fact{
					Kind: facts.KindStorage,
					Name: tableName,
					File: w.relFile,
					Line: int(node.StartPosition().Row) + 1,
					Props: map[string]any{
						"storage_kind": "table",
						"framework":    "django",
						"class":        qualName,
					},
				})
				break
			}
			if djangoCBVBases[last] {
				props["django_component"] = "view"
				props["framework"] = "django"
				break
			}
			if djangoSerializerBases[last] {
				props["django_component"] = "serializer"
				props["framework"] = "django"
				break
			}
		}
	}

	f := facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      qualName,
		File:      w.relFile,
		Line:      int(node.StartPosition().Row) + 1,
		Props:     props,
		Relations: rels,
	}

	w.out = append(w.out, f)
	w.pushOwner(len(w.out) - 1)

	bodyNode := node.ChildByFieldName("body")
	w.pushType(name, collectPyMethodNames(bodyNode, w.src))
	if bodyNode != nil {
		w.walkBody(bodyNode)
	}
	w.popType()
	w.popOwner()
}

// handleFunction emits a KindSymbol fact for a function/method.
func (w *pyWalker) handleFunction(node *sitter.Node, decorators []string) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := pyText(nameNode, w.src)
	qualName := w.module + "." + w.qualify(name)

	// Determine if this is a method (inside a class) or a top-level function.
	symbolKind := facts.SymbolFunc
	if len(w.typeStack) > 0 {
		symbolKind = facts.SymbolMethod
	}

	props := map[string]any{
		"symbol_kind": symbolKind,
		"language":    "python",
		// Leading-underscore convention marks a name as private/internal.
		"exported": !strings.HasPrefix(name, "_"),
	}
	if len(w.typeStack) > 0 {
		props["receiver"] = w.typeStack[len(w.typeStack)-1]
	}

	// async keyword: look for it as a sibling before the `def` keyword.
	fullText := pyText(node, w.src)
	if strings.HasPrefix(strings.TrimSpace(fullText), "async ") {
		props["async"] = true
	}

	// Return type.
	if retNode := node.ChildByFieldName("return_type"); retNode != nil {
		rt := strings.TrimSpace(pyText(retNode, w.src))
		if strings.HasPrefix(rt, "->") {
			rt = strings.TrimSpace(rt[2:])
		}
		if rt != "" {
			props["return_type"] = rt
		}
	}

	for _, dec := range decorators {
		applyDecoratorProps(props, dec)
	}

	rels := []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}}

	f := facts.Fact{
		Kind:      facts.KindSymbol,
		Name:      qualName,
		File:      w.relFile,
		Line:      int(node.StartPosition().Row) + 1,
		Props:     props,
		Relations: rels,
	}

	w.out = append(w.out, f)
	w.pushOwner(len(w.out) - 1)
	if bodyNode := node.ChildByFieldName("body"); bodyNode != nil {
		w.localTypes = collectParamTypes(node.ChildByFieldName("parameters"), w.src, w.importMap, w.module)
		for k, v := range collectLocalTypes(bodyNode, w.src, w.importMap, w.module) {
			w.localTypes[k] = v
		}
		// Set up per-function complexity tracking for this body walk. The props
		// map is shared by reference with the fact in w.out, so writing to it
		// after the walk updates the emitted fact.
		w.metrics = &pyBodyMetrics{}
		w.loopDepth = 0
		w.selfName = qualName
		w.walkForCalls(bodyNode)
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
		w.selfName = ""
		w.localTypes = nil
	}
	w.popOwner()
}

// handleExprStatement checks for SQLAlchemy __tablename__ assignments and
// Django urlpatterns at module/class level.
func (w *pyWalker) handleExprStatement(node *sitter.Node) {
	text := pyText(node, w.src)
	if m := tableNameRe.FindStringSubmatch(text); m != nil {
		// Find the enclosing class name for the storage fact.
		className := ""
		if len(w.typeStack) > 0 {
			className = w.module + "." + w.enclosingType()
		}
		sf := facts.Fact{
			Kind: facts.KindStorage,
			Name: m[1],
			File: w.relFile,
			Line: int(node.StartPosition().Row) + 1,
			Props: map[string]any{
				"storage_kind": "table",
				"framework":    "sqlalchemy",
			},
		}
		if className != "" {
			sf.Props["class"] = className
			sf.Relations = []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}}
		}
		w.out = append(w.out, sf)
		return
	}
	// Django urls.py: urlpatterns = [...].
	if w.isDjango && filepath.Base(w.relFile) == "urls.py" {
		for _, m := range urlPathRe.FindAllStringSubmatch(text, -1) {
			w.out = append(w.out, facts.Fact{
				Kind: facts.KindRoute,
				Name: "* " + m[1],
				File: w.relFile,
				Line: int(node.StartPosition().Row) + 1,
				Props: map[string]any{
					"role":      "server",
					"path":      m[1],
					"handler":   m[2],
					"framework": "django",
				},
			})
		}
	}
}

// handleAssignment handles module-level assignment statements (tree-sitter
// sometimes emits these as "assignment" nodes rather than "expression_statement").
func (w *pyWalker) handleAssignment(node *sitter.Node) {
	text := pyText(node, w.src)
	// Django urls.py: urlpatterns = [...].
	if w.isDjango && filepath.Base(w.relFile) == "urls.py" {
		for _, m := range urlPathRe.FindAllStringSubmatch(text, -1) {
			w.out = append(w.out, facts.Fact{
				Kind: facts.KindRoute,
				Name: "* " + m[1],
				File: w.relFile,
				Line: int(node.StartPosition().Row) + 1,
				Props: map[string]any{
					"role":      "server",
					"path":      m[1],
					"handler":   m[2],
					"framework": "django",
				},
			})
		}
	}
}

// walkBody walks a class body, dispatching each statement.
func (w *pyWalker) walkBody(body *sitter.Node) {
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		w.walkStatement(body.Child(i))
	}
}

// walkForCalls recursively scans a function body for call nodes and emits
// RelCalls / RelInstantiates on the current owner.
func (w *pyWalker) walkForCalls(node *sitter.Node) {
	if node == nil {
		return
	}
	kind := node.Kind()
	if kind == "call" {
		if fn := node.ChildByFieldName("function"); fn != nil {
			w.emitCallEdge(fn)
		}
	}

	// Complexity metrics: count decision points so the single body walk doubles
	// as the cyclomatic/loop pass (mirrors the Go extractor).
	if w.metrics != nil {
		switch kind {
		case "if_statement", "elif_clause", "conditional_expression",
			"except_clause", "case_clause", "boolean_operator":
			w.metrics.decisions++
		}
	}

	// Don't recurse into nested class/function definitions — they get their own owner.
	switch kind {
	case "class_definition", "function_definition", "decorated_definition":
		return
	}

	// Comprehensions and generator expressions carry an implicit loop, but the
	// first for-clause's iterable is evaluated once (not per-iteration), so they
	// need special handling — see walkComprehension.
	switch kind {
	case "list_comprehension", "dictionary_comprehension", "set_comprehension", "generator_expression":
		w.walkComprehension(node)
		return
	}

	// Statement loops raise the nesting depth for calls within their body.
	isLoop := kind == "for_statement" || kind == "while_statement"
	if isLoop {
		if w.metrics != nil {
			w.metrics.loopCount++
			w.metrics.decisions++
			if w.loopDepth+1 > w.metrics.loopDepth {
				w.metrics.loopDepth = w.loopDepth + 1
			}
		}
		w.loopDepth++
	}

	for i := uint(0); i < uint(node.ChildCount()); i++ {
		w.walkForCalls(node.Child(i))
	}

	if isLoop {
		w.loopDepth--
	}
}

// walkComprehension handles list/set/dict comprehensions and generator
// expressions. They carry an implicit loop (counted as one nesting level), but
// the iterable of the FIRST for-clause is evaluated exactly once — so a call
// there (e.g. `[x for x in session.query(...)]`) must NOT be counted as in-loop,
// otherwise it reads as a false N+1. The element expression, if-conditions, and
// any subsequent for-clauses do run per-iteration and are walked at inner depth.
//
// Simplification: a comprehension counts as a single loop level even when it has
// multiple for-clauses (which are really nested); this keeps depth attribution
// conservative rather than over-counting.
func (w *pyWalker) walkComprehension(node *sitter.Node) {
	if w.metrics != nil {
		w.metrics.loopCount++
		w.metrics.decisions++
		if w.loopDepth+1 > w.metrics.loopDepth {
			w.metrics.loopDepth = w.loopDepth + 1
		}
	}

	// Identify the first for-clause's iterable (its "right" field).
	var firstIter *sitter.Node
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		if c := node.Child(i); c.Kind() == "for_in_clause" {
			firstIter = c.ChildByFieldName("right")
			break
		}
	}
	if firstIter != nil {
		w.walkForCalls(firstIter) // evaluated once → stays at outer depth
	}

	w.loopDepth++
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Kind() == "for_in_clause" {
			// Walk the clause's children at inner depth, skipping the already
			// handled first iterable (matched by byte range).
			for j := uint(0); j < uint(child.ChildCount()); j++ {
				cc := child.Child(j)
				if firstIter != nil && cc.StartByte() == firstIter.StartByte() && cc.EndByte() == firstIter.EndByte() {
					continue
				}
				w.walkForCalls(cc)
			}
			continue
		}
		w.walkForCalls(child)
	}
	w.loopDepth--
}

// emitCallEdge resolves the callee node and appends a relation to the current owner.
func (w *pyWalker) emitCallEdge(fn *sitter.Node) {
	owner := w.currentOwner()
	if owner == nil {
		return
	}

	switch fn.Kind() {
	case "identifier":
		name := pyText(fn, w.src)
		if pyBuiltins[name] {
			return
		}
		if pyCapitalized(name) {
			owner.Relations = append(owner.Relations, facts.Relation{
				Kind:   facts.RelInstantiates,
				Target: name,
			})
			return
		}
		if target := w.resolveCall(name); target != "" {
			owner.Relations = append(owner.Relations, facts.Relation{
				Kind:   facts.RelCalls,
				Target: target,
			})
			w.recordCallMetrics(target)
		}

	case "attribute":
		objNode := fn.ChildByFieldName("object")
		attrNode := fn.ChildByFieldName("attribute")
		if objNode == nil || attrNode == nil {
			return
		}
		obj := pyText(objNode, w.src)
		attr := pyText(attrNode, w.src)
		if obj == "self" || obj == "cls" {
			if methods := w.currentMethods(); methods[attr] {
				target := w.module + "." + w.enclosingType() + "." + attr
				owner.Relations = append(owner.Relations, facts.Relation{
					Kind:   facts.RelCalls,
					Target: target,
				})
				w.recordCallMetrics(target)
			}
			return
		}
		// Resolve the receiver to a qualified type via localTypes or importMap.
		qualType := w.resolveVarType(obj)
		if qualType == "" {
			return
		}
		target := qualType + "." + attr
		owner.Relations = append(owner.Relations, facts.Relation{
			Kind:   facts.RelCalls,
			Target: target,
		})
		w.recordCallMetrics(target)
		w.emitImplementorCalls(owner, attr, qualType)
	}
}

// resolveVarType returns the canonical qualified type for a local variable name,
// checking localTypes first then importMap (for class-level references).
// Returns "" when the type cannot be statically determined.
func (w *pyWalker) resolveVarType(obj string) string {
	if w.localTypes != nil {
		if t, ok := w.localTypes[obj]; ok {
			return t
		}
	}
	if w.importMap != nil {
		if t, ok := w.importMap[obj]; ok && t != "" {
			return t
		}
	}
	return ""
}

// emitImplementorCalls emits additional RelCalls edges to all concrete classes
// that implement qualType, when a matching method exists on each implementor.
func (w *pyWalker) emitImplementorCalls(owner *facts.Fact, methodName, qualType string) {
	if w.idx == nil {
		return
	}
	bare := lastComponent(qualType)
	seen := make(map[string]bool)
	for _, key := range []string{bare, qualType} {
		for _, concreteQual := range w.idx.implMap[key] {
			if seen[concreteQual] {
				continue
			}
			seen[concreteQual] = true
			info, ok := w.idx.classes[concreteQual]
			if !ok || !info.methods[methodName] {
				continue
			}
			owner.Relations = append(owner.Relations, facts.Relation{
				Kind:   facts.RelCalls,
				Target: concreteQual + "." + methodName,
			})
		}
	}
}

// resolveCall maps a bare call name to a canonical fact target.
func (w *pyWalker) resolveCall(name string) string {
	// Same-class method.
	if methods := w.currentMethods(); methods[name] {
		return w.module + "." + w.enclosingType() + "." + name
	}
	// Imported name.
	if target, ok := w.importMap[name]; ok {
		return target // "" means external → no edge
	}
	// Same-module top-level function.
	return w.module + "." + name
}

// collectPyMethodNames returns the set of function names declared directly in a
// class body node.
func collectPyMethodNames(body *sitter.Node, src []byte) map[string]bool {
	methods := make(map[string]bool)
	if body == nil {
		return methods
	}
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		c := body.Child(i)
		var fn *sitter.Node
		switch c.Kind() {
		case "function_definition":
			fn = c
		case "decorated_definition":
			for j := uint(0); j < uint(c.ChildCount()); j++ {
				if c.Child(j).Kind() == "function_definition" {
					fn = c.Child(j)
					break
				}
			}
		}
		if fn != nil {
			if nameNode := fn.ChildByFieldName("name"); nameNode != nil {
				methods[pyText(nameNode, src)] = true
			}
		}
	}
	return methods
}

// bodyHasAbstractMethod reports whether a class body declares at least one
// method decorated with @abstractmethod (or @abc.abstractmethod), a reliable
// signal that the class is abstract.
func bodyHasAbstractMethod(body *sitter.Node, src []byte) bool {
	if body == nil {
		return false
	}
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		c := body.Child(i)
		if c.Kind() != "decorated_definition" {
			continue
		}
		for j := uint(0); j < uint(c.ChildCount()); j++ {
			d := c.Child(j)
			if d.Kind() != "decorator" {
				continue
			}
			if m := decoratorRe.FindStringSubmatch(pyText(d, src)); m != nil {
				if lastComponent(m[1]) == "abstractmethod" {
					return true
				}
			}
		}
	}
	return false
}

// hasDecorator reports whether any name in decorators has last as its
// last dot-separated component (e.g. "overload" matches both "overload"
// and "typing.overload").
func hasDecorator(decorators []string, last string) bool {
	for _, d := range decorators {
		if lastComponent(d) == last {
			return true
		}
	}
	return false
}

func pyFuncName(node *sitter.Node, src []byte) string {
	if n := node.ChildByFieldName("name"); n != nil {
		return pyText(n, src)
	}
	return ""
}

func pyText(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	return string(src[node.StartByte():node.EndByte()])
}

func pyCapitalized(s string) bool {
	if s == "" {
		return false
	}
	return unicode.IsUpper([]rune(s)[0])
}

// --- Type resolution helpers ---

// resolveTypeNamePy converts a raw annotation string to a canonical qualified name
// using the file's importMap. Returns "" for external/unresolvable/built-in types.
func resolveTypeNamePy(typeStr, module string, importMap map[string]string) string {
	typeStr = strings.TrimSpace(typeStr)
	typeStr = stripGenericWrapper(typeStr)
	if typeStr == "" || pyBuiltinTypes[typeStr] {
		return ""
	}
	if strings.Contains(typeStr, ".") {
		parts := strings.SplitN(typeStr, ".", 2)
		if importMap != nil {
			if t, ok := importMap[parts[0]]; ok && t != "" {
				return t + "." + parts[1]
			}
		}
		// Dotted name not in importMap — treat as external, skip.
		return ""
	}
	if importMap != nil {
		if t, ok := importMap[typeStr]; ok {
			return t // "" means external → caller drops it
		}
	}
	// Not imported and not a built-in — assume same-module class.
	return module + "." + typeStr
}

// stripGenericWrapper strips the outermost generic wrapper from a type string.
// Handles bracket generics (Optional[X], List[X], Union[X, None]) and PEP-604
// union syntax (X | None, X | Y).
func stripGenericWrapper(s string) string {
	// PEP-604 union: "X | Y" — take the first non-None part.
	if strings.Contains(s, "|") && !strings.Contains(s, "[") {
		for _, part := range strings.Split(s, "|") {
			part = strings.TrimSpace(part)
			if part != "None" && part != "" {
				return part
			}
		}
		return ""
	}
	i := strings.Index(s, "[")
	if i < 0 {
		return s
	}
	inner := strings.TrimSpace(s[i+1 : len(s)-1])
	if comma := strings.Index(inner, ","); comma >= 0 {
		part := strings.TrimSpace(inner[:comma])
		if part == "None" {
			part = strings.TrimSpace(inner[comma+1:])
		}
		return part
	}
	return inner
}

// pyBuiltinTypes is the set of Python built-in and standard-library primitive
// type names that should not be resolved to project facts.
var pyBuiltinTypes = map[string]bool{
	"str": true, "int": true, "float": true, "bool": true, "bytes": true,
	"bytearray": true, "list": true, "dict": true, "set": true, "tuple": true,
	"frozenset": true, "type": true, "object": true, "complex": true,
	"None": true, "NoneType": true, "Any": true, "Optional": true, "Union": true,
	"List": true, "Dict": true, "Set": true, "Tuple": true, "FrozenSet": true,
	"Type": true, "Callable": true, "ClassVar": true, "Final": true,
	"Literal": true, "TypeVar": true, "Generic": true, "NamedTuple": true,
	"TypedDict": true, "Iterator": true, "Generator": true, "Iterable": true,
	"AsyncIterator": true, "AsyncGenerator": true, "AsyncIterable": true,
	"Sequence": true, "MutableSequence": true, "Mapping": true,
	"MutableMapping": true, "IO": true, "TextIO": true, "BinaryIO": true,
	"Pattern": true, "Match": true, "AnyStr": true, "Text": true,
	"Awaitable": true, "Coroutine": true, "T": true,
}

// collectParamTypes scans a function parameter list and returns a map of
// param name → qualified type for all typed parameters (skipping self/cls).
func collectParamTypes(params *sitter.Node, src []byte, importMap map[string]string, module string) map[string]string {
	result := make(map[string]string)
	if params == nil {
		return result
	}
	for i := uint(0); i < uint(params.ChildCount()); i++ {
		c := params.Child(i)
		var paramName string
		var typeNode *sitter.Node
		switch c.Kind() {
		case "typed_parameter":
			// The identifier is the first child; type is a named field.
			if first := c.Child(0); first != nil && first.Kind() == "identifier" {
				paramName = pyText(first, src)
			}
			typeNode = c.ChildByFieldName("type")
		case "typed_default_parameter":
			if n := c.ChildByFieldName("name"); n != nil {
				paramName = pyText(n, src)
			}
			typeNode = c.ChildByFieldName("type")
		}
		if paramName == "" || paramName == "self" || paramName == "cls" || typeNode == nil {
			continue
		}
		if resolved := resolveTypeNamePy(pyText(typeNode, src), module, importMap); resolved != "" {
			result[paramName] = resolved
		}
	}
	return result
}

// collectLocalTypes scans a function body for type-inferable assignments and
// returns a map of local variable name → qualified type.
func collectLocalTypes(body *sitter.Node, src []byte, importMap map[string]string, module string) map[string]string {
	result := make(map[string]string)
	collectLocalTypesNode(body, src, importMap, module, result)
	return result
}

func collectLocalTypesNode(node *sitter.Node, src []byte, importMap map[string]string, module string, result map[string]string) {
	if node == nil {
		return
	}
	switch node.Kind() {
	case "function_definition", "class_definition", "decorated_definition":
		return // do not cross scope boundaries
	case "annotated_assignment":
		// x: Type  or  x: Type = value
		leftNode := node.ChildByFieldName("left")
		annotNode := node.ChildByFieldName("annotation")
		if leftNode != nil && leftNode.Kind() == "identifier" && annotNode != nil {
			name := pyText(leftNode, src)
			if resolved := resolveTypeNamePy(pyText(annotNode, src), module, importMap); resolved != "" {
				result[name] = resolved
			}
		}
		return
	case "assignment":
		// x = MyClass()
		leftNode := node.ChildByFieldName("left")
		rightNode := node.ChildByFieldName("right")
		if leftNode != nil && leftNode.Kind() == "identifier" && rightNode != nil {
			if rightNode.Kind() == "call" {
				if fnNode := rightNode.ChildByFieldName("function"); fnNode != nil && fnNode.Kind() == "identifier" {
					ctorName := pyText(fnNode, src)
					if pyCapitalized(ctorName) && !pyBuiltins[ctorName] {
						if resolved := resolveTypeNamePy(ctorName, module, importMap); resolved != "" {
							result[pyText(leftNode, src)] = resolved
						}
					}
				}
			}
		}
		return
	default:
		for i := uint(0); i < uint(node.ChildCount()); i++ {
			collectLocalTypesNode(node.Child(i), src, importMap, module, result)
		}
	}
}

// --- Multi-file symbol index ---

// pyClassInfo records what was learned about a class during the index pass.
type pyClassInfo struct {
	qualName   string
	bases      []string
	isAbstract bool
	methods    map[string]bool
}

// pySymbolIndex is the global symbol table built by the index pass.
type pySymbolIndex struct {
	classes map[string]*pyClassInfo // "module.ClassName" → info
	implMap map[string][]string     // base name → concrete implementor qual names
}

// buildFileIndex scans src for class declarations and populates idx.
// It does not emit any facts — it is read-only over the AST.
func buildFileIndex(src []byte, relFile string, idx *pySymbolIndex) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(python.Language())); err != nil {
		return
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()

	module := strings.TrimSuffix(relFile, ".py")
	root := tree.RootNode()
	for i := uint(0); i < uint(root.ChildCount()); i++ {
		node := root.Child(i)
		switch node.Kind() {
		case "class_definition":
			indexClass(node, src, module, idx)
		case "decorated_definition":
			for j := uint(0); j < uint(node.ChildCount()); j++ {
				if node.Child(j).Kind() == "class_definition" {
					indexClass(node.Child(j), src, module, idx)
					break
				}
			}
		}
	}
}

// indexClass records a class from the index pass into idx.
func indexClass(node *sitter.Node, src []byte, module string, idx *pySymbolIndex) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := pyText(nameNode, src)
	qualName := module + "." + name

	var bases []string
	isAbstract := false

	if args := node.ChildByFieldName("superclasses"); args != nil {
		for i := uint(0); i < uint(args.ChildCount()); i++ {
			c := args.Child(i)
			var base string
			switch c.Kind() {
			case "identifier":
				base = pyText(c, src)
			case "attribute":
				base = pyText(c, src)
			case "subscript":
				if valueNode := c.ChildByFieldName("value"); valueNode != nil {
					base = pyText(valueNode, src)
				}
			}
			if base != "" {
				last := lastComponent(base)
				if last == "ABC" || last == "ABCMeta" || last == "Protocol" {
					isAbstract = true
				}
				bases = append(bases, base)
			}
		}
	}

	bodyNode := node.ChildByFieldName("body")
	methods := collectPyMethodNames(bodyNode, src)

	if !isAbstract && bodyNode != nil && hasAbstractMethod(bodyNode, src) {
		isAbstract = true
	}

	idx.classes[qualName] = &pyClassInfo{
		qualName:   qualName,
		bases:      bases,
		isAbstract: isAbstract,
		methods:    methods,
	}
}

// hasAbstractMethod reports whether any method in a class body has @abstractmethod.
func hasAbstractMethod(body *sitter.Node, src []byte) bool {
	for i := uint(0); i < uint(body.ChildCount()); i++ {
		c := body.Child(i)
		if c.Kind() != "decorated_definition" {
			continue
		}
		for j := uint(0); j < uint(c.ChildCount()); j++ {
			d := c.Child(j)
			if d.Kind() == "decorator" {
				text := pyText(d, src)
				if m := decoratorRe.FindStringSubmatch(text); m != nil {
					if lastComponent(m[1]) == "abstractmethod" {
						return true
					}
				}
			}
		}
	}
	return false
}

// finalizeImplMap populates idx.implMap by inverting the implements edges:
// for each concrete class C that lists base B, C is appended to implMap[B].
func finalizeImplMap(idx *pySymbolIndex) {
	idx.implMap = make(map[string][]string)
	for qualName, info := range idx.classes {
		if info.isAbstract {
			continue
		}
		for _, base := range info.bases {
			idx.implMap[base] = append(idx.implMap[base], qualName)
		}
	}
}

// pyBuiltins are Python built-in functions that appear as bare calls without
// an import and have no local fact — resolving them would produce phantom edges.
var pyBuiltins = map[string]bool{
	"print": true, "len": true, "range": true, "enumerate": true, "zip": true,
	"map": true, "filter": true, "sorted": true, "reversed": true, "list": true,
	"dict": true, "set": true, "tuple": true, "str": true, "int": true,
	"float": true, "bool": true, "bytes": true, "type": true, "isinstance": true,
	"issubclass": true, "hasattr": true, "getattr": true, "setattr": true,
	"delattr": true, "callable": true, "repr": true, "hash": true, "id": true,
	"abs": true, "round": true, "min": true, "max": true, "sum": true,
	"any": true, "all": true, "next": true, "iter": true, "open": true,
	"super": true, "object": true, "property": true, "staticmethod": true,
	"classmethod": true, "vars": true, "dir": true, "globals": true,
	"locals": true, "exec": true, "eval": true, "compile": true,
	"input": true, "format": true, "chr": true, "ord": true, "hex": true,
	"oct": true, "bin": true, "pow": true, "divmod": true, "slice": true,
	"NotImplemented": true, "Exception": true, "ValueError": true,
	"TypeError": true, "KeyError": true, "IndexError": true,
	"AttributeError": true, "RuntimeError": true, "StopIteration": true,
	"GeneratorExit": true, "SystemExit": true, "KeyboardInterrupt": true,
}
