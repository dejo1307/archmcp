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

// Layer is one tier of a recognised taxonomy, for callers that need the ORDER
// the taxonomy defines rather than the classification it produced.
type Layer struct {
	Name    string `json:"name"`
	Level   int    `json:"level"`
	Neutral bool   `json:"neutral"`
	// Example is a module from the snapshot that sits in this layer, or empty
	// when the snapshot has none. It is what makes the order concrete: "pages,
	// then components, then composables" is a rule, and "app/pages/home, then
	// app/components/card" is the same rule in this repository's own words.
	Example string `json:"example,omitempty"`
}

// GuideFor returns the layers of the named taxonomy, OUTERMOST FIRST, each with
// an example module drawn from the given facts. Unordered layers come last, with
// Neutral set, because a reader still needs to know they exist.
//
// It exists so that the "how to add a feature" section of the rendered context is
// DERIVED rather than authored. That section used to hold hand-written prose for
// three of the taxonomies and generic filler for the rest, so a repository whose
// layering had been recognised in full — a Nuxt front end where every one of 67
// classified modules had a layer and every cross-layer import ran inward — was
// told to "identify the appropriate module/package for the feature". The order is
// already known; the guidance is a rendering of it.
//
// Matching lives here rather than in the renderer so there is one implementation
// of what a layer means. Declared patterns are not covered: their order comes from
// intent facts rather than from this file, and the caller falls back.
func GuideFor(patternName string, ff []facts.Fact) ([]Layer, bool) {
	var def *patternDef
	for i := range patternDefs {
		if patternDefs[i].name == patternName {
			def = &patternDefs[i]
			break
		}
	}
	if def == nil {
		return nil, false
	}

	modules := make([]facts.Fact, 0, len(ff))
	for _, f := range ff {
		if f.Kind == facts.KindModule && !common.IsTestModule(f) {
			modules = append(modules, f)
		}
	}
	cohort, _ := def.cohort(modules)

	out := make([]Layer, 0, len(def.layers))
	for i := range def.layers {
		layer := Layer{Name: def.layers[i].Name, Level: def.layers[i].Level, Neutral: def.layers[i].Neutral}
		for _, m := range cohort {
			// First match in the taxonomy's own precedence order, so the example
			// is a module the explainer would have put in this layer.
			if idx, ok := classifiedAs(m.Name, *def); ok && idx == i {
				layer.Example = m.Name
				break
			}
		}
		out = append(out, layer)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Neutral != out[j].Neutral {
			return !out[i].Neutral // ordered layers first
		}
		if out[i].Level != out[j].Level {
			return out[i].Level > out[j].Level // outermost first
		}
		return out[i].Name < out[j].Name
	})
	return out, true
}

// classifiedAs returns the index of the layer a module falls into, mirroring the
// classification loop in detectPatterns.
func classifiedAs(name string, def patternDef) (int, bool) {
	for i := range def.layers {
		if matchesLayerIn(name, def.layers[i].Patterns, def.dottedSegments) {
			return i, true
		}
	}
	return 0, false
}
