package rubyextractor

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/extractors/extcoverage"
	"github.com/enola-labs/enola/internal/facts"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractAllRoutes finds and parses every Rails route file in the repository, each
// under the URL prefix the file is actually served at.
//
// A route file's prefix comes from whichever construct pulled it in, and there are
// three:
//
//   - `draw(:pkg)` from a parent file, usually inside a scope('/api')/namespace(:vN)
//     block. Parsed standalone, the delegated file loses that prefix and its routes
//     read as "/devices" instead of "/api/v2/devices", matching no client call.
//   - `mount SomeEngine, at: '/x'`, which serves an entire engine's config/routes.rb
//     below /x. Resolving the constant to its directory is what makes solidus's six
//     engines and discourse's plugin engines reachable at all.
//   - nothing — the application's own config/routes.rb, at the root prefix.
//
// So the parse is ordered: the application root first, because it declares the draws
// and mounts; then the files those name, seeded with the prefix they were named under;
// then anything left over at the root prefix, so a route file that is genuinely loaded
// by a mechanism not modelled here still contributes its routes rather than vanishing.
func extractAllRoutes(repoPath string, files []string) []facts.Fact {
	idx := indexRouteFiles(files)
	if len(idx.all) == 0 {
		return nil
	}

	readSrc := func(relFile string) ([]byte, bool) {
		src, err := os.ReadFile(filepath.Join(repoPath, relFile))
		if err != nil {
			log.Printf("[ruby-extractor] error reading route file %s: %v", relFile, err)
			return nil, false
		}
		return src, true
	}

	var allFacts []facts.Fact
	parsed := map[string]bool{}
	// parse reads relFile under prefix, recording it as done. Returns what it learned so
	// the caller can follow the delegations and mounts it declares.
	parse := func(relFile, prefix string) (draws map[string]string, mounts []routeMount) {
		if parsed[relFile] {
			return nil, nil
		}
		parsed[relFile] = true
		src, ok := readSrc(relFile)
		if !ok {
			return nil, nil
		}
		ff, res := parseRouteFile(src, relFile, prefix)
		allFacts = append(allFacts, ff...)
		return res.draws, res.mounts
	}

	// Pass 1: the application's own routes.rb, at the root prefix.
	var pendingDraws map[string]string
	var pendingMounts []routeMount
	if idx.appRoot != "" {
		pendingDraws, pendingMounts = parse(idx.appRoot, "")
	}

	// Pass 2: follow draw(:pkg) delegations transitively — a delegated file may itself
	// draw further files (GitLab's config/routes/directs/*.rb are drawn from
	// config/routes/directs.rb). Keys are resolved against every config/routes/<key>.rb
	// in the repository, not just the root one, because GitLab's `draw` override loads
	// the CE and EE copy of a name together.
	for len(pendingDraws) > 0 {
		next := map[string]string{}
		for _, key := range sortedDrawKeys(pendingDraws) {
			prefix := pendingDraws[key]
			for _, target := range idx.drawTargets[key] {
				draws, mounts := parse(target, prefix)
				for k, v := range draws {
					if _, seen := next[k]; !seen {
						next[k] = v
					}
				}
				pendingMounts = append(pendingMounts, mounts...)
			}
		}
		pendingDraws = next
	}

	// Pass 3: mounted engines. Resolve each mount's constant to the directory whose
	// config/routes.rb it owns, and parse that file under the mount path.
	constants := engineConstants(repoPath, idx, files)
	for _, m := range pendingMounts {
		dir, ok := constants[normalizeConstant(m.constant)]
		if !ok {
			continue
		}
		target, ok := idx.engineRoots[dir]
		if !ok {
			continue
		}
		draws, mounts := parse(target, m.prefix)
		// An engine may draw and mount in turn; one extra level covers the real cases
		// (an engine mounting a sub-engine) without an unbounded fixpoint.
		for _, key := range sortedDrawKeys(draws) {
			for _, t := range idx.drawTargets[key] {
				parse(t, draws[key])
			}
		}
		for _, sub := range mounts {
			if d, ok := constants[normalizeConstant(sub.constant)]; ok {
				if t, ok := idx.engineRoots[d]; ok {
					parse(t, sub.prefix)
				}
			}
		}
	}

	// Pass 4: everything not reached above, at the root prefix. An engine whose mount
	// site is in a file enola cannot see (a plugin loader, an initializer) still serves
	// its routes; recording them un-prefixed is closer to the truth than recording none.
	for _, relFile := range idx.all {
		parse(relFile, "")
	}

	return allFacts
}

// sortedDrawKeys returns m's keys in sorted order, so route emission is deterministic.
func sortedDrawKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// routeScope tracks the current route scope prefix.
type routeScope struct {
	pathPrefix string
	module     string
	// memberParam is the parent member path parameter (`:<singular>_id`) that nested
	// resources declared inside a *plural* `resources` block must nest under; empty for
	// namespace/scope/singular-resource scopes, which add no member id to their children.
	memberParam string
	// controller is the Rails controller path ("api/v2/posts") that routes declared
	// directly inside this scope are served by. Set by `resources`/`resource` and by an
	// explicit `controller:` option; empty for namespace/scope, which only contribute a
	// module segment. This is what lets `get :status, on: :member` inside a resources
	// block resolve to the same controller as the resource itself.
	controller string
}

// buildPrefix constructs the current URL prefix from the scope stack.
func buildPrefix(stack []routeScope) string {
	var parts []string
	for _, s := range stack {
		if s.pathPrefix != "" {
			parts = append(parts, s.pathPrefix)
		}
	}
	return strings.Join(parts, "")
}

// buildModule joins the controller-namespace segments contributed by the scope stack
// ("api", "v2" -> "api/v2"). `namespace :api` and `scope module: :api` both add one;
// a plain `scope '/api'` adds none, because it changes the URL and not the controller
// lookup.
func buildModule(stack []routeScope) string {
	var parts []string
	for _, s := range stack {
		if s.module != "" {
			parts = append(parts, s.module)
		}
	}
	return strings.Join(parts, "/")
}

// currentController returns the innermost controller path on the stack, or "".
func currentController(stack []routeScope) string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].controller != "" {
			return stack[i].controller
		}
	}
	return ""
}

// controllerSymbol converts a Rails handler string ("api/v2/posts#index") into the
// fully-qualified Ruby symbol the ruby AST walker emits for that action
// ("Api::V2::PostsController#index"), so a route can carry a handled_by relation to a
// node that actually exists in the graph.
//
// Returns "" when the handler names no action (a bare "posts") or points at something
// that is not a controller method — a redirect, a Rack app, a lambda — because a
// relation to a node that will never exist is worse than none.
func controllerSymbol(handler string) string {
	path, action, ok := strings.Cut(handler, "#")
	if !ok || path == "" || action == "" {
		return ""
	}
	// A `to:` value can be a full class path already ("Api::V2::PostsController#index")
	// or a Rack-style constant; leave those alone rather than re-camelizing them.
	if strings.Contains(path, "::") {
		if !strings.HasSuffix(path, "Controller") {
			return ""
		}
		return path + "#" + action
	}
	if strings.ContainsAny(path, " /()#") && !isControllerPath(path) {
		return ""
	}
	var segs []string
	for _, seg := range strings.Split(path, "/") {
		if seg == "" {
			continue
		}
		segs = append(segs, snakeToCamel(seg))
	}
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "::") + "Controller#" + action
}

// firstPositionalName returns the first positional argument of a verb call when it
// names a single action — `get :status` or `get 'status'` — and "" when it is a path
// with segments or params (`get 'reports/:id/export'`), where the action cannot be
// derived from the path alone.
func firstPositionalName(args *sitter.Node, src []byte) string {
	p := firstPositionalPath(args, src)
	if p == "" || !isControllerPath(strings.TrimPrefix(p, "/")) {
		return ""
	}
	return strings.TrimPrefix(p, "/")
}

// isControllerPath reports whether s is a plain Rails controller path — lowercase
// identifiers separated by slashes, nothing else.
func isControllerPath(s string) bool {
	if s == "" {
		return false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "" {
			return false
		}
		for _, r := range seg {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
				return false
			}
		}
	}
	return true
}

// restAction describes a single RESTful action.
type restAction struct {
	name   string
	method string
	suffix string
}

// restfulActions returns the set of REST actions for a resources declaration,
// honoring only:/except: filters parsed from the declaration's arguments.
func restfulActions(only, except map[string]bool) []restAction {
	all := []restAction{
		{name: "index", method: "GET", suffix: ""},
		{name: "create", method: "POST", suffix: ""},
		{name: "new", method: "GET", suffix: "/new"},
		{name: "show", method: "GET", suffix: "/:id"},
		// Rails routes BOTH PATCH and PUT to the update action, so emit both — a
		// client calling either verb must resolve to the same served endpoint.
		{name: "update", method: "PATCH", suffix: "/:id"},
		{name: "update", method: "PUT", suffix: "/:id"},
		{name: "edit", method: "GET", suffix: "/:id/edit"},
		{name: "destroy", method: "DELETE", suffix: "/:id"},
	}

	if len(only) > 0 {
		return filterActions(all, only, true)
	}
	if len(except) > 0 {
		return filterActions(all, except, false)
	}
	return all
}

// restfulActionsSingular returns the REST actions for a singular `resource`
// declaration. A singular resource has no index and no `:id` member segment — every
// action acts on the single resource at its base path.
func restfulActionsSingular(only, except map[string]bool) []restAction {
	all := []restAction{
		{name: "create", method: "POST", suffix: ""},
		{name: "new", method: "GET", suffix: "/new"},
		{name: "show", method: "GET", suffix: ""},
		// Rails routes BOTH PATCH and PUT to update (see restfulActions).
		{name: "update", method: "PATCH", suffix: ""},
		{name: "update", method: "PUT", suffix: ""},
		{name: "edit", method: "GET", suffix: "/edit"},
		{name: "destroy", method: "DELETE", suffix: ""},
	}

	if len(only) > 0 {
		return filterActions(all, only, true)
	}
	if len(except) > 0 {
		return filterActions(all, except, false)
	}
	return all
}

// filterActions returns actions filtered by an allow or deny list.
func filterActions(all []restAction, names map[string]bool, isAllow bool) []restAction {
	var result []restAction
	for _, a := range all {
		if isAllow {
			if names[a.name] {
				result = append(result, a)
			}
		} else {
			if !names[a.name] {
				result = append(result, a)
			}
		}
	}
	return result
}

// associationCoverageFact accounts for the associations this extractor read and
// the ones whose target it could not name.
func associationCoverageFact(repoPath string, resolved int, unresolved map[string]int) (facts.Fact, bool) {
	return extcoverage.Fact(repoPath, "ruby:associations", "rails_association", resolved, unresolved)
}

// callCoverageFact accounts for call edges whose target names a symbol this
// extractor emitted, against those that name something it never did.
//
// 71% of a large monolith's 218,263 call edges point at a name that is not a
// known symbol — 37,913 distinct names, led by `params`, `include` and
// `company`. Those are not all misses: many are Rails DSL or local variables
// that were never calls. But without this they vanish, and a vanished edge is
// indistinguishable from one that was never there.
//
// The unresolved set is counted, not materialised. Turning 37,913 names into
// nodes would put `params` in the graph as a symbol, and a node nobody can
// follow is the same defect as an edge nobody can follow.
func callCoverageFact(repoPath string, resolved int, unresolved map[string]int) (facts.Fact, bool) {
	return extcoverage.Fact(repoPath, "ruby:calls", "ruby_call", resolved, unresolved)
}
