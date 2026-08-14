package phpextractor

import (
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	sitter "github.com/tree-sitter/go-tree-sitter"
	php "github.com/tree-sitter/tree-sitter-php/bindings/go"
)

// laravelVerbs are the Route:: registration methods that map directly to an HTTP
// verb. `any` and `match` are handled separately.
var laravelVerbs = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH",
	"delete": "DELETE", "options": "OPTIONS", "head": "HEAD",
}

// phpRouteScope is one frame of the Laravel route-group prefix/name stack.
type phpRouteScope struct {
	prefix string
	name   string
}

// extractLaravelRoutes parses a Laravel routes file (routes/web.php, routes/api.php,
// …) and emits a server-route fact per registered route. It understands verb calls
// (Route::get/post/…), Route::match / Route::any, Route::resource / apiResource
// expansion, fluent and array-based route groups with prefix accumulation, and the
// ->name(…) modifier.
func extractLaravelRoutes(src []byte, relFile string) []facts.Fact {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(php.LanguagePHP())); err != nil {
		return nil
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()

	w := &laravelRouteWalker{src: src, relFile: relFile, dir: filepath.Dir(relFile)}
	w.walk(tree.RootNode(), nil)
	return w.out
}

type laravelRouteWalker struct {
	src     []byte
	relFile string
	dir     string
	out     []facts.Fact
}

// chainCall is one link of a fluent Route chain: a method name and its argument list.
type chainCall struct {
	method string
	args   *sitter.Node
	node   *sitter.Node
}

// walk descends the tree, handling Route:: chains specially (so their nested scoped
// calls are not re-processed) and recursing elsewhere with the active scope stack.
func (w *laravelRouteWalker) walk(node *sitter.Node, stack []phpRouteScope) {
	if node == nil {
		return
	}
	if kindOf(node) == "scoped_call_expression" || kindOf(node) == "member_call_expression" {
		if calls, ok := linearizeRouteChain(node, w.src); ok {
			w.handleChain(node, calls, stack)
			return
		}
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		w.walk(node.Child(i), stack)
	}
}

// linearizeRouteChain flattens a fluent call chain into root-first order and reports
// whether it is rooted at the Route facade (Route::… or Route::…()->…()).
func linearizeRouteChain(node *sitter.Node, src []byte) ([]chainCall, bool) {
	var rev []chainCall
	cur := node
	for cur != nil {
		switch kindOf(cur) {
		case "member_call_expression", "nullsafe_member_call_expression":
			name := cur.ChildByFieldName("name")
			rev = append(rev, chainCall{phpText(name, src), cur.ChildByFieldName("arguments"), cur})
			cur = cur.ChildByFieldName("object")
		case "scoped_call_expression":
			scope := cur.ChildByFieldName("scope")
			name := cur.ChildByFieldName("name")
			rev = append(rev, chainCall{phpText(name, src), cur.ChildByFieldName("arguments"), cur})
			if phpText(scope, src) != "Route" {
				return nil, false
			}
			// Reverse into root-first order.
			calls := make([]chainCall, len(rev))
			for i := range rev {
				calls[len(rev)-1-i] = rev[i]
			}
			return calls, true
		default:
			return nil, false
		}
	}
	return nil, false
}

// handleChain processes a Route facade chain: it emits route(s) for a verb / match /
// any / resource registration, or pushes a group's prefix+name and walks the closure
// body for Route::group / fluent ->group.
func (w *laravelRouteWalker) handleChain(node *sitter.Node, calls []chainCall, stack []phpRouteScope) {
	// Collect fluent modifiers shared across the chain.
	routeName := ""
	fluentPrefix := ""
	groupIdx := -1
	for i, c := range calls {
		switch c.method {
		case "name":
			routeName = positionalString(c.args, 0, w.src)
		case "prefix":
			fluentPrefix = positionalString(c.args, 0, w.src)
		case "group":
			groupIdx = i
		}
	}

	if groupIdx >= 0 {
		prefix, name := fluentPrefix, ""
		// A leading array argument (Route::group(['prefix'=>…,'as'=>…], …)) also
		// contributes prefix/name.
		if opts := positionalArg(calls[groupIdx].args, 0); opts != nil && kindOf(opts) == "array_creation_expression" {
			kv := arrayKeyValues(opts, w.src)
			if p := kv["prefix"]; p != "" {
				prefix = p
			}
			name = kv["as"]
		}
		newStack := append(append([]phpRouteScope{}, stack...), phpRouteScope{prefix: prefix, name: name})
		if body := groupClosureBody(calls[groupIdx].args); body != nil {
			w.walk(body, newStack)
		}
		return
	}

	// Otherwise this is a route registration: find the primary verb/match/any/resource call.
	for _, c := range calls {
		switch {
		case laravelVerbs[c.method] != "":
			w.emitRoute(c.node, stack, laravelVerbs[c.method], pathArg(c.args, w.src), laravelHandler(positionalArg(c.args, 1), w.src), routeName, "")
		case c.method == "any":
			w.emitRoute(c.node, stack, "ANY", pathArg(c.args, w.src), laravelHandler(positionalArg(c.args, 1), w.src), routeName, "")
		case c.method == "match":
			verbs := arrayStringElements(positionalArg(c.args, 0), w.src)
			path := positionalString(c.args, 1, w.src)
			handler := laravelHandler(positionalArg(c.args, 2), w.src)
			for _, v := range verbs {
				w.emitRoute(c.node, stack, strings.ToUpper(v), path, handler, routeName, "")
			}
		case c.method == "resource" || c.method == "apiResource":
			w.emitResource(c.node, stack, c.args, c.method == "apiResource")
		}
	}
}

// emitResource expands a Route::resource / apiResource declaration into its REST
// actions, mirroring Laravel's resource controller conventions.
func (w *laravelRouteWalker) emitResource(node *sitter.Node, stack []phpRouteScope, args *sitter.Node, apiOnly bool) {
	base := positionalString(args, 0, w.src)
	if base == "" {
		return
	}
	controller := resourceController(positionalArg(args, 1), w.src)
	type action struct {
		name, method, suffix string
	}
	actions := []action{
		{"index", "GET", ""},
		{"create", "GET", "/create"},
		{"store", "POST", ""},
		{"show", "GET", "/{id}"},
		{"edit", "GET", "/{id}/edit"},
		{"update", "PUT", "/{id}"},
		{"destroy", "DELETE", "/{id}"},
	}
	for _, a := range actions {
		if apiOnly && (a.name == "create" || a.name == "edit") {
			continue // apiResource omits the HTML form endpoints
		}
		handler := ""
		if controller != "" {
			handler = controller + "::" + a.name
		}
		w.emitRoute(node, stack, a.method, "/"+strings.Trim(base, "/")+a.suffix, handler, "", a.name)
	}
}

// emitRoute appends one server-route fact, joining the active group prefixes with the
// route path.
func (w *laravelRouteWalker) emitRoute(node *sitter.Node, stack []phpRouteScope, method, rawPath, handler, name, action string) {
	if method == "" {
		return
	}
	path := buildLaravelPath(stack, rawPath)
	if path == "" {
		return
	}
	props := map[string]any{
		facts.PropRole: facts.RoleServer,
		"method":       method,
		"framework":    "laravel",
		"language":     "php",
		"path":         path,
	}
	if handler != "" {
		props["handler"] = handler
	}
	if name != "" {
		props["name"] = name
	}
	if action != "" {
		props["action"] = action
	}
	w.out = append(w.out, facts.Fact{
		Kind:      facts.KindRoute,
		Name:      path,
		File:      w.relFile,
		Line:      line(node),
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: w.dir}},
	})
}

// buildLaravelPath joins the group-prefix stack with a route's raw path into a single
// normalized "/a/b/c" path.
func buildLaravelPath(stack []phpRouteScope, raw string) string {
	var segs []string
	for _, s := range stack {
		segs = append(segs, splitSegs(s.prefix)...)
	}
	segs = append(segs, splitSegs(raw)...)
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}

// splitSegs splits a path fragment into non-empty segments.
func splitSegs(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// pathArg returns the first positional argument of a verb call as a path string.
func pathArg(args *sitter.Node, src []byte) string {
	return positionalString(args, 0, src)
}

// laravelHandler renders a Laravel route action argument as "Controller::method":
// a string 'Ctrl@method' or '\App\Ctrl@method', or an array [Ctrl::class, 'method'].
// A closure or unrecognized form yields "".
func laravelHandler(arg *sitter.Node, src []byte) string {
	if arg == nil {
		return ""
	}
	switch kindOf(arg) {
	case "string", "encapsed_string":
		s := stringLiteral(arg, src)
		if i := strings.IndexByte(s, '@'); i >= 0 {
			return strings.TrimLeft(s[:i], "\\") + "::" + s[i+1:]
		}
		return ""
	case "array_creation_expression":
		var ctrl, method string
		for i := uint(0); i < arg.ChildCount(); i++ {
			el := arg.Child(i)
			if kindOf(el) != "array_element_initializer" {
				continue
			}
			for j := uint(0); j < el.ChildCount(); j++ {
				gc := el.Child(j)
				if kindOf(gc) == "class_constant_access_expression" {
					ctrl = classConstClass(gc, src)
				} else if s := stringLiteral(gc, src); s != "" {
					method = s
				}
			}
		}
		if ctrl != "" && method != "" {
			return ctrl + "::" + method
		}
	}
	return ""
}

// resourceController renders the controller of a Route::resource declaration:
// PhotoController::class -> "PhotoController", or a string 'PhotoController'.
func resourceController(arg *sitter.Node, src []byte) string {
	if arg == nil {
		return ""
	}
	switch kindOf(arg) {
	case "class_constant_access_expression":
		return classConstClass(arg, src)
	case "string", "encapsed_string":
		return strings.TrimLeft(stringLiteral(arg, src), "\\")
	}
	return ""
}

// classConstClass returns the class name of a `Foo::class` expression.
func classConstClass(node *sitter.Node, src []byte) string {
	for i := uint(0); i < node.ChildCount(); i++ {
		if c := node.Child(i); kindOf(c) == "name" || kindOf(c) == "qualified_name" {
			return strings.TrimLeft(phpText(c, src), "\\")
		}
	}
	return ""
}

// groupClosureBody returns the body node of a group's closure argument
// (anonymous_function -> compound_statement, arrow_function -> expression), or nil.
func groupClosureBody(args *sitter.Node) *sitter.Node {
	if args == nil {
		return nil
	}
	for i := uint(0); i < args.ChildCount(); i++ {
		a := args.Child(i)
		if kindOf(a) != "argument" {
			continue
		}
		v := argValue(a)
		if v == nil {
			continue
		}
		if kindOf(v) == "anonymous_function" || kindOf(v) == "arrow_function" {
			return v.ChildByFieldName("body")
		}
	}
	return nil
}

// arrayStringElements returns the plain string values of an array literal
// (['get','post'] -> ["get","post"]).
func arrayStringElements(node *sitter.Node, src []byte) []string {
	if node == nil || kindOf(node) != "array_creation_expression" {
		return nil
	}
	var out []string
	for i := uint(0); i < node.ChildCount(); i++ {
		el := node.Child(i)
		if kindOf(el) != "array_element_initializer" {
			continue
		}
		for j := uint(0); j < el.ChildCount(); j++ {
			if s := stringLiteral(el.Child(j), src); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// arrayKeyValues returns the string key=>value pairs of an array literal
// (['prefix'=>'api','as'=>'admin.'] -> {"prefix":"api","as":"admin."}).
func arrayKeyValues(node *sitter.Node, src []byte) map[string]string {
	out := map[string]string{}
	if node == nil || kindOf(node) != "array_creation_expression" {
		return out
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		el := node.Child(i)
		if kindOf(el) != "array_element_initializer" {
			continue
		}
		var strs []string
		for j := uint(0); j < el.ChildCount(); j++ {
			if s := stringLiteral(el.Child(j), src); s != "" {
				strs = append(strs, s)
			}
		}
		if len(strs) >= 2 {
			out[strs[0]] = strs[1]
		}
	}
	return out
}
