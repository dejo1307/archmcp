package rubyextractor

import (
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	sitter "github.com/tree-sitter/go-tree-sitter"
	ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
)

// parseRouteFileAST parses a Rails route file with tree-sitter and emits KindRoute
// facts. Block boundaries come from the grammar (do_block) rather than counting
// `do`/`end`, so nested namespaces/resources/scopes are tracked precisely.
func parseRouteFileAST(src []byte, relFile string) []facts.Fact {
	ff, _ := parseRouteFile(src, relFile, "")
	return ff
}

// routeMount records a `mount SomeEngine, at: '/x'` site: the constant that was
// mounted and the full URL prefix it was mounted under (including any enclosing
// scope/namespace). extractAllRoutes resolves the constant to the engine directory
// whose config/routes.rb should be parsed below that prefix.
type routeMount struct {
	constant string
	prefix   string
}

// routeFileResult is what parsing one route file taught us about the OTHER files that
// make up the same route table.
type routeFileResult struct {
	// draws maps each draw(:pkg) delegation found in this file to the URL prefix it is
	// scoped under, so the caller can parse config/routes/<pkg>.rb with it.
	draws map[string]string
	// mounts lists the engine mount sites found in this file.
	mounts []routeMount
}

// parseRouteFile parses a Rails route file, seeding the scope stack with
// initialPrefix (the URL prefix under which a parent routes.rb delegated or mounted
// this file), and returns what it learned about further route files.
func parseRouteFile(src []byte, relFile, initialPrefix string) ([]facts.Fact, routeFileResult) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(ruby.Language())); err != nil {
		return nil, routeFileResult{}
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()

	var stack []routeScope
	if initialPrefix != "" {
		stack = []routeScope{{pathPrefix: initialPrefix}}
	}
	rw := &routeWalker{
		src:      src,
		relFile:  relFile,
		dir:      filepath.Dir(relFile),
		draws:    map[string]string{},
		concerns: map[string]*sitter.Node{},
	}
	rw.walk(tree.RootNode(), stack)
	return rw.out, routeFileResult{draws: rw.draws, mounts: rw.mounts}
}

type routeWalker struct {
	src     []byte
	relFile string
	dir     string
	out     []facts.Fact
	// draws maps each draw(:pkg) delegation found in this file to the URL prefix it
	// is scoped under, so the caller can parse config/routes/<pkg>.rb with it.
	draws map[string]string
	// mounts collects the engine mount sites found in this file.
	mounts []routeMount
	// concerns maps a `concern :name do ... end` definition to its block body, so a
	// later `concerns: :name` can replay it under the scope that referenced it. Rails
	// requires the definition to precede the reference, so a single forward pass is
	// enough.
	concerns map[string]*sitter.Node
	// concernDepth bounds concern replay. A concern that references itself would
	// otherwise recurse forever.
	concernDepth int
}

// walk iterates the statements of a program / body_statement, dispatching each
// route-DSL call with the current scope stack.
//
// It also descends through plain Ruby control flow, because a route file is Ruby and
// real ones are full of it: GitLab guards whole files with `unless
// @organization_scoped_routes` and `if Rails.env.development?`, solidus wraps its admin
// routes in `if SolidusSupport.admin_available?`, and Rails' own activestorage route
// file is one `draw do … end` closed by an `if` MODIFIER. Iterating only direct `call`
// children silently dropped every one of them — five whole route files across the
// corpus, and the failure is invisible because a file that parses fine and yields
// nothing looks exactly like a file with no routes.
//
// Both branches of a conditional are walked. Which one Rails takes depends on runtime
// configuration the extractor cannot see, and a route that exists under some
// configuration is a route.
func (rw *routeWalker) walk(node *sitter.Node, stack []routeScope) {
	if node == nil {
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		c := node.Child(i)
		if kindOf(c) == "call" {
			rw.handleCall(c, stack)
			continue
		}
		if isControlFlowNode(kindOf(c)) {
			rw.walkControlFlow(c, stack)
		}
	}
}

// walkControlFlow descends into a conditional or block-structured statement, skipping
// its CONDITION — `if Rails.env.development?` would otherwise be dispatched as a route
// DSL call named `development?`.
func (rw *routeWalker) walkControlFlow(node *sitter.Node, stack []routeScope) {
	cond := node.ChildByFieldName("condition")
	for i := uint(0); i < node.ChildCount(); i++ {
		c := node.Child(i)
		if cond != nil && c.Id() == cond.Id() {
			continue
		}
		if kindOf(c) == "call" {
			rw.handleCall(c, stack)
			continue
		}
		if isControlFlowNode(kindOf(c)) {
			rw.walkControlFlow(c, stack)
		}
	}
}

// isControlFlowNode reports whether a node kind is Ruby control flow whose body may
// contain route declarations.
func isControlFlowNode(kind string) bool {
	switch kind {
	case "if", "unless", "elsif", "else", "then", "if_modifier", "unless_modifier",
		"case", "when", "begin", "ensure", "rescue", "body_statement",
		"parenthesized_statements", "while", "until", "each":
		return true
	}
	return false
}

// blockBody returns the body_statement of a call's do/brace block, or nil.
func blockBody(call *sitter.Node) *sitter.Node {
	block := call.ChildByFieldName("block")
	if block == nil {
		return nil
	}
	return block.ChildByFieldName("body")
}

func (rw *routeWalker) handleCall(call *sitter.Node, stack []routeScope) {
	method := rubyText(call.ChildByFieldName("method"), rw.src)
	args := call.ChildByFieldName("arguments")
	body := blockBody(call)
	prefix := buildPrefix(stack)

	switch method {
	case "get", "post", "put", "patch", "delete":
		// Accept both a string path ('cities_by_zip') and a bare symbol (:cities_by_zip);
		// the positional helper also avoids picking up a `to:` handler string.
		path := firstPositionalPath(args, rw.src)
		if path == "" {
			return
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		// A bare verb directly inside a plural `resources` block nests under the
		// parent member id (Rails serves `resources :steps do get :status end` at
		// /steps/:step_id/status), as does `on: :member`; only `on: :collection`
		// stays at the collection path. Explicit member/collection blocks push
		// their own scope, whose memberParam is empty, so nothing doubles.
		if len(stack) > 0 {
			if mp := stack[len(stack)-1].memberParam; mp != "" {
				if pairSymbol(args, "on", rw.src) != "collection" {
					path = "/:" + mp + path
				}
			}
		}
		props := map[string]any{
			"method":    strings.ToUpper(method),
			"framework": "rails",
			"language":  "ruby",
		}
		handler := pairString(args, "to", rw.src)
		if handler == "" {
			// The hash-rocket form `get 'path' => 'ctrl#action'`, where the handler is
			// the VALUE of the pair whose key is the path. Discourse and lobsters write
			// nearly every route this way, and reading only `to:` left thousands of
			// routes with no handler at all.
			handler = hashRocketHandler(args, rw.src)
		}
		if handler == "" {
			// Still nothing. Inside a resources/controller block the verb names an action
			// on the enclosing controller (`resources :posts do get :publish, on: :member
			// end` -> posts#publish), which is how the majority of non-REST Rails routes
			// are written. Outside one there is nothing to derive from.
			if ctrl := currentController(stack); ctrl != "" {
				if action := firstPositionalName(args, rw.src); action != "" {
					handler = ctrl + "#" + action
				}
			}
		}
		if handler != "" {
			props["handler"] = handler
		}
		// A Rails optional segment `foo(/:bar)` serves two paths; emit both.
		for _, p := range expandOptionalSegments(path) {
			rw.emit(prefix+p, line(call), props)
		}

	case "root":
		handler := pairString(args, "to", rw.src)
		if handler == "" {
			handler = firstStringArg(args, rw.src)
		}
		props := map[string]any{
			"method":    "GET",
			"framework": "rails",
			"language":  "ruby",
		}
		if handler != "" {
			props["handler"] = handler
		}
		rw.emit(prefix+"/", line(call), props)

	case "resources", "resource":
		// Symbol or string, as for `namespace` above.
		name := firstSymbolArg(args, rw.src)
		if name == "" {
			name = strings.Trim(firstStringArg(args, rw.src), "/")
		}
		if name == "" {
			return
		}
		only := pairSymbols(args, "only", rw.src)
		except := pairSymbols(args, "except", rw.src)
		singular := method == "resource"

		// A resource nested inside a *plural* `resources` block nests under the parent
		// member (`/widgets/:widget_id/...`); the parent supplies that param via the
		// enclosing scope's memberParam. parentScopePrefix is the parent resource's own
		// path segment, needed to compute the shallow path below.
		parentMember := ""
		parentScopePrefix := ""
		if len(stack) > 0 {
			if p := stack[len(stack)-1].memberParam; p != "" {
				parentMember = "/:" + p
			}
			parentScopePrefix = stack[len(stack)-1].pathPrefix
		}
		// `path:` overrides the URL segment while the resource name still drives the
		// props and the nested member param (Rails derives `:name_id` from the name).
		segmentName := name
		if p := pairString(args, "path", rw.src); p != "" {
			segmentName = strings.Trim(p, "/")
		}
		segment := parentMember + "/" + segmentName
		resourcePath := prefix + segment

		// Rails `shallow: true` serves a nested plural resource's MEMBER routes
		// (show/edit/update/destroy) at a shallow path — the parent resource segment
		// and its member param are dropped — while the collection routes
		// (index/create/new) stay nested. shallowBase is the prefix with the parent
		// resource segment stripped, plus this resource's own segment.
		shallow := !singular && parentMember != "" && pairBool(args, "shallow", rw.src)
		shallowBase := strings.TrimSuffix(prefix, parentScopePrefix) + "/" + segmentName

		// The controller a resource is served by: the enclosing module namespace plus the
		// resource's own name, pluralized (Rails maps both `resources :posts` and the
		// singular `resource :profile` onto a PLURAL controller), unless `controller:`
		// names one explicitly.
		controller := pluralize(name)
		if c := pairString(args, "controller", rw.src); c != "" {
			controller = strings.Trim(c, "/")
		} else if c := pairSymbol(args, "controller", rw.src); c != "" {
			controller = c
		}
		if mod := buildModule(stack); mod != "" && !strings.Contains(controller, "/") {
			controller = mod + "/" + controller
		}

		actions := restfulActions(only, except)
		if singular {
			actions = restfulActionsSingular(only, except)
		}
		for _, a := range actions {
			routePath := resourcePath + a.suffix
			if shallow && strings.HasPrefix(a.suffix, "/:id") {
				routePath = shallowBase + a.suffix
			}
			rw.emit(routePath, line(call), map[string]any{
				"method":    a.method,
				"framework": "rails",
				"language":  "ruby",
				"resource":  name,
				"action":    a.name,
				"handler":   controller + "#" + a.name,
			})
		}
		// A plural resource exposes a member id to its children; a singular one does not.
		childMember := ""
		if !singular {
			childMember = singularize(name) + "_id"
		}
		childScope := routeScope{pathPrefix: segment, memberParam: childMember, controller: controller}
		if body != nil {
			rw.walk(body, append(stack, childScope))
		}
		// `resources :posts, concerns: :commentable` replays the named concern's block
		// inside the resource's own scope, exactly as if it had been written inline.
		rw.replayConcerns(concernNames(args, rw.src), append(stack, childScope))

	case "match":
		// `match 'x', via: [:get, :post]` maps one path to several verbs; emit one
		// route per listed verb. Without a `via:` the verb set is ambiguous (older
		// Rails defaulted to all), so emit nothing rather than guess.
		path := firstPositionalPath(args, rw.src)
		if path == "" {
			return
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		handler := pairString(args, "to", rw.src)
		for _, v := range symbolValues(findPairValue(args, "via", rw.src), rw.src) {
			verb := strings.ToUpper(v)
			if verb == "" || verb == "ALL" {
				continue // via: :all matches every verb — not a concrete route
			}
			props := map[string]any{
				"method":    verb,
				"framework": "rails",
				"language":  "ruby",
			}
			if handler != "" {
				props["handler"] = handler
			}
			for _, p := range expandOptionalSegments(path) {
				rw.emit(prefix+p, line(call), props)
			}
		}

	case "namespace":
		// Rails accepts a symbol or a string: `namespace :admin` and
		// `namespace "recaptcha"` are the same declaration. Reading only the symbol form
		// made the whole block invisible, taking every route inside it with it.
		name := firstSymbolArg(args, rw.src)
		if name == "" {
			name = firstStringArg(args, rw.src)
		}
		if name == "" || body == nil {
			return
		}
		// `path:` overrides the URL segment (module stays the symbol name), e.g.
		// `namespace :admin, path: 'administration'`.
		pathSeg := "/" + name
		if p := pairString(args, "path", rw.src); p != "" {
			if !strings.HasPrefix(p, "/") {
				p = "/" + p
			}
			pathSeg = p
		}
		rw.walk(body, append(stack, routeScope{pathPrefix: pathSeg, module: name}))

	case "scope":
		// module: and path: are independent — a scope may set either or both. Read a
		// positional string path or a `path:` keyword for the URL prefix, and `module:`
		// for the controller namespace.
		ns := routeScope{}
		path := firstStringArg(args, rw.src)
		if path == "" {
			path = pairString(args, "path", rw.src)
		}
		if path == "" {
			// A bare positional symbol is a path prefix: `scope :users` == `scope
			// path: 'users'`. (module: is a keyword pair, so it is not read here.)
			path = firstSymbolArg(args, rw.src)
		}
		if path != "" {
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			ns.pathPrefix = path
		}
		if mod := pairSymbol(args, "module", rw.src); mod != "" {
			ns.module = mod
		}
		if body != nil {
			rw.walk(body, append(stack, ns))
		}

	case "member", "collection":
		memberPrefix := ""
		if method == "member" {
			memberPrefix = "/:id"
		}
		if body != nil {
			rw.walk(body, append(stack, routeScope{pathPrefix: memberPrefix}))
		}

	case "controller":
		// `controller :photos do get 'search' end` fixes the controller for its block
		// without changing the URL.
		name := firstSymbolArg(args, rw.src)
		if name == "" {
			name = firstStringArg(args, rw.src)
		}
		if name == "" || body == nil {
			return
		}
		if mod := buildModule(stack); mod != "" && !strings.Contains(name, "/") {
			name = mod + "/" + name
		}
		rw.walk(body, append(stack, routeScope{controller: name}))

	case "concern":
		// `concern :commentable do ... end` DEFINES a reusable block; it serves nothing
		// on its own. Record the body so each `concerns: :commentable` reference can
		// replay it under the scope that referenced it, and do NOT descend here — the
		// routes inside belong to the referencing scopes, not to this definition site.
		if name := firstSymbolArg(args, rw.src); name != "" && body != nil {
			rw.concerns[name] = body
		}

	case "concerns":
		// The bare block form: `resources :posts do concerns :commentable end`.
		rw.replayConcerns(symbolArgs(args, rw.src), stack)

	case "mount":
		// `mount SomeEngine, at: '/x'` or `mount SomeEngine => '/x'`. The engine's whole
		// route table is served below the mount path, so record the site for
		// extractAllRoutes to resolve; the placeholder route below keeps the mount
		// itself visible in the graph.
		constant, at := parseMount(args, rw.src)
		if constant == "" {
			return
		}
		if !strings.HasPrefix(at, "/") {
			at = "/" + at
		}
		mountPrefix := strings.TrimSuffix(prefix+at, "/")
		rw.mounts = append(rw.mounts, routeMount{constant: constant, prefix: mountPrefix})
		// Method "MOUNT" is inert to the cross-repo linker for the same reason "DRAW"
		// is: it is a mount point, not an endpoint a client can call.
		rw.out = append(rw.out, facts.Fact{
			Kind: facts.KindRoute,
			Name: mountPrefix + "/",
			File: rw.relFile,
			Line: line(call),
			Props: map[string]any{
				"method":    "MOUNT",
				"framework": "rails",
				"language":  "ruby",
				"mounts":    constant,
			},
			Relations: []facts.Relation{
				{Kind: facts.RelDeclares, Target: rw.dir},
				{Kind: facts.RelHandledBy, Target: normalizeConstant(constant)},
			},
		})

	case "draw":
		// `draw do ... end` is the routes wrapper (Rails.application.routes.draw,
		// engine routers, etc.) — recurse into the block. `draw(:pkg)` with no
		// block is a packwerk delegation — emit a DRAW route.
		if body != nil {
			rw.walk(body, stack)
			return
		}
		if pkg := firstSymbolArg(args, rw.src); pkg != "" {
			// Record the delegation so the caller can parse config/routes/<pkg>.rb
			// seeded with this prefix, giving its routes their real /api/vN scope.
			if rw.draws != nil {
				rw.draws[pkg] = prefix
			}
			// A DRAW placeholder route is still emitted (it backs route helpers); the
			// linker treats method "DRAW" as inert, so it never matches or is flagged.
			rw.out = append(rw.out, facts.Fact{
				Kind: facts.KindRoute,
				Name: prefix + "/" + pkg,
				File: rw.relFile,
				Line: line(call),
				Props: map[string]any{
					"method":    "DRAW",
					"framework": "rails",
					"language":  "ruby",
					"delegate":  pkg,
				},
			})
		}

	default:
		// Unknown DSL call (constraints, concern, authenticate, ...) — descend into
		// any block so nested routes are still discovered.
		if body != nil {
			rw.walk(body, stack)
		}
	}
}

// emit appends a route fact with a declares relation to the file's directory, plus a
// handled_by relation to the controller action serving it when the handler resolves to
// one. That second edge is what makes a Rails route reachable from its controller:
// without it a route is an isolated node, impact analysis from a controller cannot see
// the endpoints it serves, and a controller referenced only by the route table looks
// like dead code.
func (rw *routeWalker) emit(name string, lineNum int, props map[string]any) {
	rels := []facts.Relation{{Kind: facts.RelDeclares, Target: rw.dir}}
	if h, _ := props["handler"].(string); h != "" {
		if sym := controllerSymbol(h); sym != "" {
			rels = append(rels, facts.Relation{Kind: facts.RelHandledBy, Target: sym})
		}
	}
	rw.out = append(rw.out, facts.Fact{
		Kind:      facts.KindRoute,
		Name:      name,
		File:      rw.relFile,
		Line:      lineNum,
		Props:     props,
		Relations: rels,
	})
}

// --- mounts and concerns ---

// parseMount reads the two shapes Rails accepts for a mount:
//
//	mount Spree::Core::Engine, at: '/'
//	mount Sidekiq::Web => '/sidekiq'
//
// and returns the mounted constant with the path it is mounted at. A Rack app built
// inline (`mount Coverband::Reporters::Web.new, at: '/coverage'`) yields its receiver
// constant, which is the thing worth recording. The path defaults to "/" — Rails'
// own default when `at:` is omitted.
func parseMount(args *sitter.Node, src []byte) (constant, at string) {
	if args == nil {
		return "", ""
	}
	at = pairString(args, "at", src)
	for i := uint(0); i < args.ChildCount(); i++ {
		c := args.Child(i)
		switch kindOf(c) {
		case "constant", "scope_resolution":
			if constant == "" {
				constant = rubyText(c, src)
			}
		case "call":
			// `Foo::Bar.new` — the receiver is the constant.
			if r := c.ChildByFieldName("receiver"); r != nil && constant == "" {
				switch kindOf(r) {
				case "constant", "scope_resolution":
					constant = rubyText(r, src)
				}
			}
		case "pair":
			// Hash-rocket form: the KEY is the constant, the value the path.
			k := c.ChildByFieldName("key")
			if k == nil {
				continue
			}
			switch kindOf(k) {
			case "constant", "scope_resolution":
				if constant == "" {
					constant = rubyText(k, src)
				}
				if v := c.ChildByFieldName("value"); v != nil && at == "" {
					at = firstStringArg(v, src)
				}
			}
		}
	}
	if constant == "" {
		return "", ""
	}
	if at == "" {
		at = "/"
	}
	return constant, at
}

// hashRocketHandler reads the handler out of the hash-rocket route form
// `get 'path' => 'controller#action'`, where the path is the pair's KEY and the handler
// its VALUE.
//
// Only a plain string value is accepted. `get 'x' => redirect('/y')` and
// `get 'x' => SomeRackApp` are also legal and name no controller action, so returning
// their text would produce a handled_by edge to a node that never exists.
func hashRocketHandler(args *sitter.Node, src []byte) string {
	if args == nil {
		return ""
	}
	for i := uint(0); i < args.ChildCount(); i++ {
		c := args.Child(i)
		if kindOf(c) != "pair" {
			continue
		}
		k := c.ChildByFieldName("key")
		if k == nil || kindOf(k) != "string" {
			continue
		}
		v := c.ChildByFieldName("value")
		if v == nil || kindOf(v) != "string" {
			continue
		}
		return firstStringArg(v, src)
	}
	return ""
}

// concernNames returns the names referenced by a `concerns:` option, which takes either
// a single symbol or an array of them.
func concernNames(args *sitter.Node, src []byte) []string {
	return symbolValues(findPairValue(args, "concerns", src), src)
}

// replayConcerns re-walks each named concern's recorded block under stack, so its routes
// are emitted once per referencing scope with that scope's prefix and controller.
//
// maxConcernDepth bounds the recursion: a concern that references itself, directly or
// through another, would otherwise never terminate. Two levels covers concerns composed
// of concerns, which is as deep as the idiom goes in practice.
const maxConcernDepth = 2

func (rw *routeWalker) replayConcerns(names []string, stack []routeScope) {
	if len(names) == 0 || rw.concernDepth >= maxConcernDepth {
		return
	}
	rw.concernDepth++
	defer func() { rw.concernDepth-- }()
	for _, n := range names {
		if body := rw.concerns[n]; body != nil {
			rw.walk(body, stack)
		}
	}
}

// --- keyword-argument helpers ---

// pairBool reports whether args contains a `key: true` pair (e.g. `shallow: true`).
func pairBool(args *sitter.Node, key string, src []byte) bool {
	v := findPairValue(args, key, src)
	return v != nil && rubyText(v, src) == "true"
}

// expandOptionalSegments expands a Rails optional route segment `foo(/:bar)` into
// the concrete paths it serves — one with the optional groups omitted and one with
// them included: `email_subscriptions(/:key)` → ["/email_subscriptions",
// "/email_subscriptions/:key"]. A path with no optional group is returned as-is.
// (Multiple optional groups collapse to the all-omitted and all-included variants,
// which covers the trailing-optional idiom without an exponential blow-up.)
func expandOptionalSegments(path string) []string {
	if !strings.Contains(path, "(") {
		return []string{path}
	}
	included := strings.NewReplacer("(", "", ")", "").Replace(path)
	var b strings.Builder
	depth := 0
	for _, r := range path {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	omitted := b.String()
	if omitted == included {
		return []string{included}
	}
	return []string{omitted, included}
}

// pairString returns the string content of a `key: "value"` pair.
func pairString(args *sitter.Node, key string, src []byte) string {
	if v := findPairValue(args, key, src); v != nil {
		return firstStringArg(v, src)
	}
	return ""
}

// pairSymbol returns the symbol name of a `key: :value` pair.
func pairSymbol(args *sitter.Node, key string, src []byte) string {
	if v := findPairValue(args, key, src); v != nil && kindOf(v) == "simple_symbol" {
		return strings.TrimPrefix(rubyText(v, src), ":")
	}
	return ""
}

// pairSymbols returns the symbol names of a `key: [:a, :b]` pair.
func pairSymbols(args *sitter.Node, key string, src []byte) map[string]bool {
	out := make(map[string]bool)
	v := findPairValue(args, key, src)
	if v == nil {
		return out
	}
	for i := uint(0); i < v.ChildCount(); i++ {
		if kindOf(v.Child(i)) == "simple_symbol" {
			out[strings.TrimPrefix(rubyText(v.Child(i), src), ":")] = true
		}
	}
	return out
}

// symbolValues returns the symbol names of a value node that is either a single
// `:sym` or an array `[:a, :b]` — handling both shapes `via:` takes (pairSymbols
// only reads the array form).
func symbolValues(v *sitter.Node, src []byte) []string {
	if v == nil {
		return nil
	}
	if kindOf(v) == "simple_symbol" {
		return []string{strings.TrimPrefix(rubyText(v, src), ":")}
	}
	var out []string
	for i := uint(0); i < v.ChildCount(); i++ {
		if kindOf(v.Child(i)) == "simple_symbol" {
			out = append(out, strings.TrimPrefix(rubyText(v.Child(i), src), ":"))
		}
	}
	return out
}

// findPairValue returns the value node of a `key: value` pair in an argument_list.
func findPairValue(args *sitter.Node, key string, src []byte) *sitter.Node {
	if args == nil {
		return nil
	}
	for i := uint(0); i < args.ChildCount(); i++ {
		c := args.Child(i)
		if kindOf(c) != "pair" {
			continue
		}
		k := c.ChildByFieldName("key")
		if k != nil && strings.TrimSuffix(rubyText(k, src), ":") == key {
			return c.ChildByFieldName("value")
		}
	}
	return nil
}

// positionalSymbols returns the direct symbol arguments of a call, ignoring
// keyword pairs: the `:a, :b` of `concerns :a, :b`.
func positionalSymbols(args *sitter.Node, src []byte) []string {
	if args == nil {
		return nil
	}
	var out []string
	for i := uint(0); i < args.ChildCount(); i++ {
		child := args.Child(i)
		if child.Kind() == "simple_symbol" {
			out = append(out, strings.TrimPrefix(rubyText(child, src), ":"))
		}
	}
	return out
}
