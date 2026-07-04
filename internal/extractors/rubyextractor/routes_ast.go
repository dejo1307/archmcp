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

// parseRouteFile parses a Rails route file, seeding the scope stack with
// initialPrefix (the URL prefix a parent routes.rb delegated this file under via
// draw(:pkg)), and additionally returns the draw(:pkg) -> prefix map discovered in
// this file, so the caller can inline each delegated file under its real scope.
func parseRouteFile(src []byte, relFile, initialPrefix string) ([]facts.Fact, map[string]string) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(ruby.Language())); err != nil {
		return nil, nil
	}
	tree := parser.Parse(src, nil)
	defer tree.Close()

	var stack []routeScope
	if initialPrefix != "" {
		stack = []routeScope{{pathPrefix: initialPrefix}}
	}
	rw := &routeWalker{src: src, relFile: relFile, dir: filepath.Dir(relFile), draws: map[string]string{}}
	rw.walk(tree.RootNode(), stack)
	return rw.out, rw.draws
}

type routeWalker struct {
	src     []byte
	relFile string
	dir     string
	out     []facts.Fact
	// draws maps each draw(:pkg) delegation found in this file to the URL prefix it
	// is scoped under, so the caller can parse config/routes/<pkg>.rb with it.
	draws map[string]string
}

// walk iterates the statements of a program / body_statement, dispatching each
// route-DSL call with the current scope stack.
func (rw *routeWalker) walk(node *sitter.Node, stack []routeScope) {
	if node == nil {
		return
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		c := node.Child(i)
		if c.Kind() == "call" {
			rw.handleCall(c, stack)
		}
	}
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
		path := firstStringArg(args, rw.src)
		if path == "" {
			return
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		props := map[string]any{
			"method":    strings.ToUpper(method),
			"framework": "rails",
			"language":  "ruby",
		}
		if handler := pairString(args, "to", rw.src); handler != "" {
			props["handler"] = handler
		}
		rw.emit(prefix+path, line(call), props)

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
		name := firstSymbolArg(args, rw.src)
		if name == "" {
			return
		}
		only := pairSymbols(args, "only", rw.src)
		except := pairSymbols(args, "except", rw.src)
		singular := method == "resource"

		// A resource nested inside a *plural* `resources` block nests under the parent
		// member (`/widgets/:widget_id/...`); the parent supplies that param via the
		// enclosing scope's memberParam.
		parentMember := ""
		if len(stack) > 0 {
			if p := stack[len(stack)-1].memberParam; p != "" {
				parentMember = "/:" + p
			}
		}
		segment := parentMember + "/" + name
		resourcePath := prefix + segment

		actions := restfulActions(only, except)
		if singular {
			actions = restfulActionsSingular(only, except)
		}
		for _, a := range actions {
			rw.emit(resourcePath+a.suffix, line(call), map[string]any{
				"method":    a.method,
				"framework": "rails",
				"language":  "ruby",
				"resource":  name,
				"action":    a.name,
			})
		}
		if body != nil {
			// A plural resource exposes a member id to its children; a singular one does not.
			childMember := ""
			if !singular {
				childMember = singularize(name) + "_id"
			}
			rw.walk(body, append(stack, routeScope{pathPrefix: segment, memberParam: childMember}))
		}

	case "namespace":
		name := firstSymbolArg(args, rw.src)
		if name == "" || body == nil {
			return
		}
		rw.walk(body, append(stack, routeScope{pathPrefix: "/" + name, module: name}))

	case "scope":
		ns := routeScope{}
		if path := firstStringArg(args, rw.src); path != "" {
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			ns.pathPrefix = path
		} else if mod := pairSymbol(args, "module", rw.src); mod != "" {
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

// emit appends a route fact with a declares relation to the file's directory.
func (rw *routeWalker) emit(name string, lineNum int, props map[string]any) {
	rw.out = append(rw.out, facts.Fact{
		Kind:      facts.KindRoute,
		Name:      name,
		File:      rw.relFile,
		Line:      lineNum,
		Props:     props,
		Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: rw.dir}},
	})
}

// --- keyword-argument helpers ---

// pairString returns the string content of a `key: "value"` pair.
func pairString(args *sitter.Node, key string, src []byte) string {
	if v := findPairValue(args, key, src); v != nil {
		return firstStringArg(v, src)
	}
	return ""
}

// pairSymbol returns the symbol name of a `key: :value` pair.
func pairSymbol(args *sitter.Node, key string, src []byte) string {
	if v := findPairValue(args, key, src); v != nil && v.Kind() == "simple_symbol" {
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
		if v.Child(i).Kind() == "simple_symbol" {
			out[strings.TrimPrefix(rubyText(v.Child(i), src), ":")] = true
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
		if c.Kind() != "pair" {
			continue
		}
		k := c.ChildByFieldName("key")
		if k != nil && strings.TrimSuffix(rubyText(k, src), ":") == key {
			return c.ChildByFieldName("value")
		}
	}
	return nil
}
