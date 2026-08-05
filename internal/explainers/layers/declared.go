package layers

import (
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// Declared layer patterns — the layer-intent half of intent compilation. A
// repo whose intent facts declare layers gets its pattern built from the
// declaration at confidence 1.0 (a declared taxonomy is stated, not
// recognised) and verdicted by the SAME violation machinery the heuristic
// patterns use; repos without declarations keep the heuristic path untouched.
//
// Ordering is outermost-first: earlier entries may depend on later ones,
// never the reverse. Level runs opposite to declaration order (the last,
// innermost entry has the lowest level) so the existing wrong-direction test
// reads unchanged.
//
// Glob support is deliberately the ceiling the layer-intent decision set:
// an exact module path, or a `prefix/**` subtree — nothing more.

// declaredPatterns builds one archPattern per repo that declares layers.
func declaredPatterns(store *facts.Store) []*archPattern {
	type entry struct {
		name  string
		order int
		paths []string
	}
	byRepo := map[string][]entry{}
	for _, f := range store.ByKind(facts.KindIntent) {
		if f.PropString("intent_kind") != "layer" {
			continue
		}
		order := 0
		if o, ok := f.Props["order"].(int); ok {
			order = o
		} else if o, ok := f.Props["order"].(float64); ok {
			order = int(o)
		}
		paths, _ := f.Props["paths"].([]string)
		if paths == nil {
			if raw, ok := f.Props["paths"].([]any); ok {
				for _, p := range raw {
					if sp, ok := p.(string); ok {
						paths = append(paths, sp)
					}
				}
			}
		}
		owner := f.PropString("intent_owner")
		if owner == "" {
			owner = f.Repo
		}
		byRepo[owner] = append(byRepo[owner], entry{name: f.PropString("layer_name"), order: order, paths: paths})
	}
	if len(byRepo) == 0 {
		return nil
	}

	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	var patterns []*archPattern
	for _, repo := range repos {
		entries := byRepo[repo]
		sort.Slice(entries, func(i, j int) bool { return entries[i].order < entries[j].order })
		layerDefs := map[string]*layerDef{}
		for _, en := range entries {
			layerDefs[en.name] = &layerDef{
				Name:     en.name,
				Patterns: en.paths,
				// Outermost-first declaration order, and Level's existing meaning is
				// lower = inner: the last declared (innermost) entry gets level 0.
				Level: len(entries) - 1 - en.order,
			}
		}
		modules := map[string]string{}
		for _, m := range store.ByKind(facts.KindModule) {
			if m.Repo != repo {
				continue
			}
			stripped := strings.TrimPrefix(m.Name, repo+"/")
			for _, en := range entries {
				if matchDeclaredLayerPath(stripped, en.paths) {
					modules[m.Name] = en.name
					break
				}
			}
		}
		patterns = append(patterns, &archPattern{
			Name:       fmt.Sprintf("declared (%s)", repo),
			Confidence: 1.0,
			Layers:     layerDefs,
			Modules:    modules,
		})
	}
	return patterns
}

// matchDeclaredLayerPath applies the bounded glob dialect: exact module path,
// or `prefix/**` matching the prefix's whole subtree (and the prefix itself).
func matchDeclaredLayerPath(module string, globs []string) bool {
	for _, g := range globs {
		if prefix, ok := strings.CutSuffix(g, "/**"); ok {
			if module == prefix || strings.HasPrefix(module, prefix+"/") {
				return true
			}
			continue
		}
		if module == g {
			return true
		}
	}
	return false
}
