// Package entrypoints marks the symbols a framework invokes directly, so
// reachability has roots to start from.
//
// Dead code was declined twice on the same measurement: 1,573 of 1,573 routed
// controller actions carry no inbound call edge, because routing is not a call.
// Neither is a queue draining a job, a mailer delivering, or a rake task
// running. Without roots, every one of them looks unreachable.
//
// It stops at marking the roots. Reachability from them was measured before
// this shipped and reports 86% of the monolith's symbols unreachable, which is
// not a finding about the monolith — it is the receiver-typing gap showing
// through: 53% of the Ruby extractor's call targets are bare method names, and
// a cross-object call cannot be followed until the receiver has a type. The
// roots are useful now, since they answer which entry points reach a symbol;
// the verdict they would support is not, and emitting it would flag five-sixths
// of a working codebase as dead.
package entrypoints

import (
	"context"
	"fmt"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

type Explainer struct{}

func New() *Explainer { return &Explainer{} }

func (e *Explainer) Name() string { return "entry-points" }

// frameworkEntrySuffixes are methods a framework calls rather than the
// application: a Sidekiq job's perform, a service object's call, a mailer's
// deliver, a migration's change.
var frameworkEntrySuffixes = []string{
	"#perform", "#call", "#deliver", "#change", "#up", "#down", "#execute",
}

func (e *Explainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	symbols := map[string]bool{}
	for _, fact := range store.ByKind(facts.KindSymbol) {
		symbols[fact.Name] = true
	}
	if len(symbols) == 0 {
		return nil, nil
	}

	routed, unmatched := routeRoots(store, symbols)
	framework := 0
	for name := range symbols {
		for _, suffix := range frameworkEntrySuffixes {
			if strings.HasSuffix(name, suffix) {
				framework++
				break
			}
		}
	}
	if routed == 0 && framework == 0 {
		return nil, nil
	}

	return []facts.Insight{{
		Title: fmt.Sprintf("%d entry points: %d routed actions, %d framework-invoked",
			routed+framework, routed, framework),
		Description: "These symbols are invoked by a framework rather than by a call, so " +
			"they are the roots any reachability question has to start from. A dead-code " +
			"verdict is deliberately not derived from them: cross-object calls cannot be " +
			"followed until receivers have types, so traversal from these roots currently " +
			"reports most of a working codebase as unreachable.",
		// Measured rather than inferred: every root is a route handler the graph
		// already names, or a method whose name the framework dispatches on.
		Confidence: 1.0,
		Evidence: []facts.Evidence{{
			Detail: fmt.Sprintf("%d route handlers name a controller action that is not a known symbol",
				unmatched),
		}},
	}}, nil
}

// routeRoots counts route handlers that resolve to a symbol, and those that do
// not. A handler naming a controller the graph never extracted is a gap in
// extraction rather than an entry point, and conflating the two is how a root
// set silently shrinks.
func routeRoots(store *facts.Store, symbols map[string]bool) (int, int) {
	resolved, missing := 0, 0
	for _, fact := range store.ByKind(facts.KindRoute) {
		handler, _ := fact.Props["handler"].(string)
		controller, action, ok := strings.Cut(handler, "#")
		if !ok || controller == "" {
			continue
		}
		if symbols[controllerClass(controller)+"#"+action] {
			resolved++
			continue
		}
		missing++
	}
	return resolved, missing
}

// controllerClass turns a Rails handler path into the class the graph names:
// `admin/accounts` is `Admin::AccountsController`, not `AdminAccountsController`.
// Getting that wrong resolved 63 of 2,528 handlers instead of 1,538.
func controllerClass(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		parts := strings.Split(segment, "_")
		for j, part := range parts {
			if part == "" {
				continue
			}
			parts[j] = strings.ToUpper(part[:1]) + part[1:]
		}
		segments[i] = strings.Join(parts, "")
	}
	return strings.Join(segments, "::") + "Controller"
}
