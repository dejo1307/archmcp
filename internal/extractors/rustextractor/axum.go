package rustextractor

import (
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// axumHTTPMethods are the Axum `MethodRouter` builder functions (`get`, `post`,
// …) that appear as the callee of a `.route(path, <chain>)` call's second
// argument, e.g. `.route("/users", get(list_users).post(create_user))`.
var axumHTTPMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true, "patch": true,
	"head": true, "options": true, "trace": true, "connect": true, "any": true,
}

// axumRouteEntry is one (method, handler) pair extracted from a MethodRouter
// builder chain.
type axumRouteEntry struct {
	method  string
	handler string
}

// extractAxumRoutes walks a whole parsed file for Axum-style
// `<router>.route("/path", get(handler))` registrations and emits one
// facts.KindRoute per (path, method) pair found.
//
// Detection is purely structural — a `.route(...)` call whose first argument
// is a string literal and second argument is (a chain of) HTTP-verb builder
// calls — rather than gated on first confirming the file's crate depends on
// axum. That combination is distinctive enough in practice (mirrors how the
// Python extractor's FastAPI decorator regex doesn't verify the import either)
// and avoids needing per-crate dependency plumbing through the whole walker
// just for this one signal.
//
// *Scope:* `.route_service(...)` (a tower `Service` value, not a plain handler
// function) and `.nest(...)` (nested sub-routers, so a mounted sub-router's
// routes don't get its parent's path prefix) are not handled — a nested
// router's own routes are still found, just without the prefix applied.
func extractAxumRoutes(root *sitter.Node, src []byte, relFile, dir string) []facts.Fact {
	var out []facts.Fact
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node == nil {
			return
		}
		if node.Kind() == "call_expression" {
			if fn := node.ChildByFieldName("function"); fn != nil && fn.Kind() == "field_expression" {
				if field := fn.ChildByFieldName("field"); field != nil && nodeText(field, src) == "route" {
					out = append(out, axumRouteFacts(node, src, relFile, dir)...)
				}
			}
		}
		for i := uint(0); i < uint(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}
	walk(root)
	return out
}

// axumRouteFacts converts one `.route(path, chain)` call_expression into its
// route facts (one per HTTP verb in the chain), or nil if it doesn't match the
// expected two-argument (string literal, verb-builder chain) shape.
func axumRouteFacts(call *sitter.Node, src []byte, relFile, dir string) []facts.Fact {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	var named []*sitter.Node
	for i := uint(0); i < uint(args.ChildCount()); i++ {
		if c := args.Child(i); c.IsNamed() {
			named = append(named, c)
		}
	}
	if len(named) != 2 {
		return nil
	}
	path, ok := stringLiteralValue(named[0], src)
	if !ok {
		return nil
	}
	entries := collectMethodRouterChain(named[1], src)
	if len(entries) == 0 {
		return nil
	}

	line := int(call.StartPosition().Row) + 1
	out := make([]facts.Fact, 0, len(entries))
	for _, e := range entries {
		props := map[string]any{
			"method":    e.method,
			"framework": "axum",
			"language":  "rust",
		}
		if e.handler != "" {
			props["handler"] = e.handler
		}
		out = append(out, facts.Fact{
			Kind:      facts.KindRoute,
			Name:      path,
			File:      relFile,
			Line:      line,
			Props:     props,
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}
	return out
}

// collectMethodRouterChain walks a MethodRouter-building expression like
// `get(handler1).post(handler2)` and returns one entry per HTTP verb in the
// chain, base call first. A bare `get(handler)` (no chaining) is the base
// case; a chained `.post(handler2)` is itself a call_expression whose function
// is a field_expression wrapping the inner chain, recursed into via `value`.
func collectMethodRouterChain(node *sitter.Node, src []byte) []axumRouteEntry {
	if node == nil || node.Kind() != "call_expression" {
		return nil
	}
	fn := node.ChildByFieldName("function")
	if fn == nil {
		return nil
	}
	switch fn.Kind() {
	case "identifier":
		name := nodeText(fn, src)
		if !axumHTTPMethods[name] {
			return nil
		}
		return []axumRouteEntry{{method: strings.ToUpper(name), handler: axumFirstArgName(node, src)}}
	case "field_expression":
		field := fn.ChildByFieldName("field")
		if field == nil {
			return nil
		}
		name := nodeText(field, src)
		if !axumHTTPMethods[name] {
			return nil
		}
		entries := collectMethodRouterChain(fn.ChildByFieldName("value"), src)
		return append(entries, axumRouteEntry{method: strings.ToUpper(name), handler: axumFirstArgName(node, src)})
	}
	return nil
}

// axumFirstArgName returns a verb-builder call's handler argument as source
// text — a bare identifier ("list_users") or a qualified path
// ("handlers::list_users") — or "" for anything else (a closure, a method
// value, etc.), which is a fine best-effort miss: the route fact is still
// emitted, just without a "handler" prop.
func axumFirstArgName(call *sitter.Node, src []byte) string {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	for i := uint(0); i < uint(args.ChildCount()); i++ {
		c := args.Child(i)
		if !c.IsNamed() {
			continue
		}
		switch c.Kind() {
		case "identifier", "scoped_identifier":
			return nodeText(c, src)
		}
		return ""
	}
	return ""
}

// stringLiteralValue returns a string_literal/raw_string_literal node's
// content (escape sequences included verbatim, not unescaped — fine for a
// route path).
func stringLiteralValue(node *sitter.Node, src []byte) (string, bool) {
	if node == nil {
		return "", false
	}
	switch node.Kind() {
	case "string_literal", "raw_string_literal":
	default:
		return "", false
	}
	var b strings.Builder
	for i := uint(0); i < uint(node.ChildCount()); i++ {
		if c := node.Child(i); c.Kind() == "string_content" {
			b.WriteString(nodeText(c, src))
		}
	}
	return b.String(), true
}
