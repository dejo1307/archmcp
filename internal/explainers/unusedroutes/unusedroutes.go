// Package unusedroutes provides an explainer that summarizes server routes which
// no loaded client repo calls — endpoints the engine's cross-repo link pass
// flagged with the "unmatched_by_clients" prop — into "unused endpoint" candidate
// insights.
//
// The flag is computed structurally from the loaded snapshot only, so this
// explainer is deliberately careful to frame results as *candidates*: a route is
// listed because no client in the snapshot calls it, which is not the same as
// dead. Consumers outside the snapshot — admin/ops scripts, cron jobs, webhooks,
// third-party API clients, mobile deep links — do not appear here, so each
// candidate must be verified against those before removal.
package unusedroutes

import (
	"context"
	"fmt"
	"sort"

	"github.com/enola-labs/enola/internal/facts"
)

// maxSamples bounds how many route names an insight lists inline.
const maxSamples = 25

// Explainer emits one insight per repo whose server routes include endpoints that
// no loaded client calls.
type Explainer struct{}

// New creates a new Explainer.
func New() *Explainer { return &Explainer{} }

func (e *Explainer) Name() string { return "unused-routes" }

// Explain groups client-unmatched server routes by repo and emits one insight per
// repo, each carrying the mandatory out-of-snapshot caveat. It returns nothing for
// single-repo snapshots (no service nodes, so the cross-repo flagging never ran).
func (e *Explainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	if len(store.ByKind(facts.KindService)) == 0 {
		return nil, nil
	}

	byRepo := map[string][]string{}
	for _, f := range store.ByKind(facts.KindRoute) {
		if f.Props == nil || f.Props["unmatched_by_clients"] != true {
			continue
		}
		repo := f.Repo
		if repo == "" {
			repo = "(unlabeled)"
		}
		byRepo[repo] = append(byRepo[repo], routeLabel(f))
	}
	if len(byRepo) == 0 {
		return nil, nil
	}

	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	var insights []facts.Insight
	for _, repo := range repos {
		routes := byRepo[repo]
		sort.Strings(routes)
		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Unused endpoint candidates: %d route(s) in %s have no caller among loaded clients",
				len(routes), repo),
			Description: fmt.Sprintf("%d server route(s) in %s matched no client call site in any loaded repo. "+
				"These are candidates unused by the loaded clients ONLY — the snapshot cannot see consumers it "+
				"does not contain (admin/ops scripts, cron jobs, webhooks, third-party API clients, mobile deep "+
				"links). Verify each against those before removing the endpoint. Samples: %s.",
				len(routes), repo, sampleList(routes)),
			Confidence: 0.6,
			Evidence:   evidenceFor(routes),
			Actions: []string{
				"Verify each candidate against consumers not in the snapshot before removing the endpoint",
				"Append any missing client repo and regenerate to shrink the list to truly-unused routes",
			},
		})
	}
	return insights, nil
}

// routeLabel renders a route fact as "METHOD /path" when a method prop is present,
// else just its name.
func routeLabel(f facts.Fact) string {
	if m, ok := f.Props["method"].(string); ok && m != "" {
		return m + " " + f.Name
	}
	return f.Name
}

// sampleList renders up to maxSamples route labels, noting how many were elided.
func sampleList(routes []string) string {
	if len(routes) > maxSamples {
		return fmt.Sprintf("%v (+%d more)", routes[:maxSamples], len(routes)-maxSamples)
	}
	return fmt.Sprintf("%v", routes)
}

// evidenceFor attaches up to maxSamples route names as insight evidence.
func evidenceFor(routes []string) []facts.Evidence {
	n := len(routes)
	if n > maxSamples {
		n = maxSamples
	}
	out := make([]facts.Evidence, 0, n)
	for _, r := range routes[:n] {
		out = append(out, facts.Evidence{Fact: r, Detail: "no loaded client calls this route"})
	}
	return out
}
