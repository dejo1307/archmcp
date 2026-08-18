package layers

import (
	"sort"

	"github.com/enola-labs/enola/internal/explainers/common"
	"github.com/enola-labs/enola/internal/facts"
)

// The authoring-time half of declared layers, shaped exactly like the constraints
// explainer's MemberCounts: what does each declared layer actually SELECT in the
// snapshot on disk?
//
// `constraints lint` already answered that question for components, which is why a
// component whose match patterns stopped matching is caught while it is being
// written. Layers had no equivalent, so the only report of a layer order selecting
// nothing was a number buried in a finding that otherwise announced the order was
// in force — the shape of issue #242. The counts belong here rather than in the
// command, so that lint and the explainer resolve membership through the same
// code and cannot come to different answers about the same declaration.

// LayerCount is one declared layer resolved against the snapshot.
type LayerCount struct {
	Repo    string   `json:"repo"`
	Layer   string   `json:"layer"`
	Order   int      `json:"order"`
	Members int      `json:"members"`
	Paths   []string `json:"paths"`
}

// MemberCounts resolves every declared layer in the store against the modules the
// store measured, outermost layer first within each repo.
//
// Test modules are excluded, exactly as explainRepo excludes them: they are not
// architecture, and counting them here would report a layer as populated that the
// explainer treats as empty.
func MemberCounts(store *facts.Store) []LayerCount {
	scopes := store.RepoLabels()
	sort.Strings(scopes)
	scopes = append(scopes, "")

	var out []LayerCount
	for _, repo := range scopes {
		for _, dp := range declaredPatterns(store, repo) {
			counts := map[string]int{}
			for _, layer := range dp.Modules {
				counts[layer]++
			}
			named := make([]*layerDef, 0, len(dp.Layers))
			for _, def := range dp.Layers {
				named = append(named, def)
			}
			// Level runs opposite to declaration order (see declaredPatterns), so
			// descending level is the order the author wrote — outermost first.
			sort.Slice(named, func(i, j int) bool {
				if named[i].Level != named[j].Level {
					return named[i].Level > named[j].Level
				}
				return named[i].Name < named[j].Name
			})
			for i, def := range named {
				out = append(out, LayerCount{
					Repo:    repo,
					Layer:   def.Name,
					Order:   i,
					Members: counts[def.Name],
					Paths:   append([]string(nil), def.Patterns...),
				})
			}
		}
	}
	return out
}

// ModuleNames returns the module paths a repo measured, sorted — what a declared
// layer path has to match. Lint prints a sample when nothing matched, because the
// mismatch is almost always obvious once the two lists are read side by side and
// otherwise requires querying the fact store by hand.
func ModuleNames(store *facts.Store, repo string) []string {
	var out []string
	for _, m := range scopeFacts(store, repo) {
		if m.Kind != facts.KindModule || common.IsTestModule(m) {
			continue
		}
		out = append(out, m.Name)
	}
	sort.Strings(out)
	return out
}
