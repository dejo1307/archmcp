package dashboard

import (
	"sort"
	"strings"

	"github.com/enola-labs/enola/pkg/facts"
)

const moduleGraphLimit = 36

type moduleNode struct {
	ID                        int
	Name, Display, File, Repo string
	X, Y                      int
	FanIn, FanOut             int
	Degree                    int
}

type moduleEdge struct {
	Source, Target int
	X1, Y1, X2, Y2 int
}

type moduleGraphView struct {
	Width, Height int
	Nodes         []moduleNode
	Edges         []moduleEdge
	AllModules    []string
	Total         int
	Limited       bool
	Focused       bool
	FocusName     string
}

// buildModuleGraph produces a deliberately bounded architecture map. It ranks
// production modules by connectedness, keeps the most structurally relevant
// ones, and renders only imports whose endpoints are both visible.
func buildModuleGraph(store *facts.Store) *moduleGraphView {
	return buildModuleGraphFocused(store, "")
}

func buildModuleGraphFocused(store *facts.Store, focus string) *moduleGraphView {
	if store == nil {
		return nil
	}
	mods := store.ByKind(facts.KindModule)
	if len(mods) == 0 {
		return nil
	}

	type raw struct {
		name, file, repo string
		out              map[string]bool
		in               int
	}
	byName := make(map[string]*raw, len(mods))
	for _, m := range mods {
		if role, _ := m.Props[facts.PropModuleRole].(string); role == facts.ModuleRoleTest {
			continue
		}
		if _, exists := byName[m.Name]; !exists {
			byName[m.Name] = &raw{name: m.Name, file: m.File, repo: m.Repo, out: map[string]bool{}}
		}
	}
	for _, m := range mods {
		r := byName[m.Name]
		if r == nil {
			continue
		}
		// Snapshot stores publish a built graph whose module→module import edges are
		// synthesized from dependency facts. A small test/embedder store may not have
		// built that index yet, so fall back to relations carried directly by modules.
		type importEdge struct{ kind, target string }
		edges := make([]importEdge, 0)
		if graph := store.Graph(); graph != nil {
			for _, edge := range graph.ForwardEdges(m.Name) {
				edges = append(edges, importEdge{kind: edge.RelKind, target: edge.Target})
			}
		} else {
			for _, rel := range m.Relations {
				edges = append(edges, importEdge{kind: rel.Kind, target: rel.Target})
			}
		}
		for _, edge := range edges {
			if edge.kind != facts.RelImports || edge.target == m.Name || byName[edge.target] == nil || r.out[edge.target] {
				continue
			}
			r.out[edge.target] = true
			byName[edge.target].in++
		}
	}

	ranked := make([]*raw, 0, len(byName))
	for _, r := range byName {
		if r.in+len(r.out) > 0 {
			ranked = append(ranked, r)
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		di, dj := ranked[i].in+len(ranked[i].out), ranked[j].in+len(ranked[j].out)
		if di != dj {
			return di > dj
		}
		return ranked[i].name < ranked[j].name
	})
	if len(ranked) == 0 {
		return nil
	}
	total := len(ranked)
	allModules := make([]string, 0, len(ranked))
	for _, r := range ranked {
		allModules = append(allModules, r.name)
	}
	focused := false
	if center := byName[focus]; focus != "" && center != nil {
		neighborhood := map[string]bool{focus: true}
		for target := range center.out {
			neighborhood[target] = true
		}
		for _, r := range ranked {
			if r.out[focus] {
				neighborhood[r.name] = true
			}
		}
		selected := []*raw{center}
		for _, r := range ranked {
			if r.name != focus && neighborhood[r.name] {
				selected = append(selected, r)
			}
		}
		ranked = selected
		focused = true
	}
	if len(ranked) > moduleGraphLimit {
		ranked = ranked[:moduleGraphLimit]
	}

	const cols, nodeW, nodeH, gapX, gapY, margin = 4, 154, 36, 34, 34, 28
	rows := (len(ranked) + cols - 1) / cols
	view := &moduleGraphView{Width: margin*2 + cols*nodeW + (cols-1)*gapX, Height: margin*2 + rows*nodeH + (rows-1)*gapY, AllModules: allModules, Total: total, Limited: total > len(ranked), Focused: focused, FocusName: focus}
	index := make(map[string]int, len(ranked))
	for i, r := range ranked {
		x := margin + (i%cols)*(nodeW+gapX)
		y := margin + (i/cols)*(nodeH+gapY)
		view.Nodes = append(view.Nodes, moduleNode{ID: i, Name: r.name, Display: moduleLabel(r.name), File: r.file, Repo: r.repo, X: x, Y: y, FanIn: r.in, FanOut: len(r.out), Degree: r.in + len(r.out)})
		index[r.name] = i
	}
	for i, r := range ranked {
		for target := range r.out {
			if focused && r.name != focus && target != focus {
				continue
			}
			j, ok := index[target]
			if !ok {
				continue
			}
			a, b := view.Nodes[i], view.Nodes[j]
			view.Edges = append(view.Edges, moduleEdge{Source: i, Target: j, X1: a.X + nodeW/2, Y1: a.Y + nodeH/2, X2: b.X + nodeW/2, Y2: b.Y + nodeH/2})
		}
	}
	sort.Slice(view.Edges, func(i, j int) bool {
		if view.Edges[i].Source != view.Edges[j].Source {
			return view.Edges[i].Source < view.Edges[j].Source
		}
		return view.Edges[i].Target < view.Edges[j].Target
	})
	return view
}

func moduleLabel(name string) string {
	const limit = 25
	if len(name) <= limit {
		return name
	}
	parts := strings.Split(name, "/")
	if len(parts) > 1 {
		tail := parts[len(parts)-2] + "/" + parts[len(parts)-1]
		if len(tail) <= limit {
			return tail
		}
	}
	return "…" + name[len(name)-(limit-1):]
}
