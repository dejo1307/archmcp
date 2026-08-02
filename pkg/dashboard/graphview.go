package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/enola-labs/enola/pkg/facts"
)

// graphView is the browser contract for the graph inspector. Nodes and edges
// contain the complete current graph; OverviewNodes/OverviewEdges are the
// smaller module-level projection rendered first by the browser.
type graphView struct {
	SnapshotID    string      `json:"snapshot_id,omitempty"`
	Nodes         []graphNode `json:"nodes"`
	Edges         []graphEdge `json:"edges"`
	OverviewNodes []graphNode `json:"overview_nodes"`
	OverviewEdges []graphEdge `json:"overview_edges"`
	NodeKinds     []string    `json:"node_kinds"`
	EdgeKinds     []string    `json:"edge_kinds"`
	FactCount     int         `json:"fact_count"`
	EdgeCount     int         `json:"edge_count"`
	Unresolved    int         `json:"unresolved"`
}

type graphNode struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Label      string         `json:"label"`
	Kind       string         `json:"kind"`
	Repo       string         `json:"repo,omitempty"`
	File       string         `json:"file,omitempty"`
	Line       int            `json:"line,omitempty"`
	Count      int            `json:"count,omitempty"`
	Props      map[string]any `json:"props,omitempty"`
	Unresolved bool           `json:"unresolved,omitempty"`
	Conflated  []string       `json:"conflated,omitempty"`
}

type graphEdge struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Kind       string `json:"kind"`
	Count      int    `json:"count"`
	Aggregated bool   `json:"aggregated,omitempty"`
}

type focusedGraph struct {
	SnapshotID string      `json:"snapshot_id,omitempty"`
	Scope      string      `json:"scope"`
	Focus      string      `json:"focus,omitempty"`
	Nodes      []graphNode `json:"nodes"`
	Edges      []graphEdge `json:"edges"`
	Truncated  bool        `json:"truncated"`
	HasMore    bool        `json:"has_more"`
	NodeCount  int         `json:"node_count"`
	EdgeCount  int         `json:"edge_count"`
}

type graphBootstrap struct {
	SnapshotID    string      `json:"snapshot_id,omitempty"`
	OverviewNodes []graphNode `json:"overview_nodes"`
	OverviewEdges []graphEdge `json:"overview_edges"`
	NodeKinds     []string    `json:"node_kinds"`
	EdgeKinds     []string    `json:"edge_kinds"`
	FactCount     int         `json:"fact_count"`
	EdgeCount     int         `json:"edge_count"`
	Unresolved    int         `json:"unresolved"`
}

// buildGraphView creates a deterministic, JSON-safe graph payload. The fact
// index is used to give each fact a stable-in-response ID instead of treating a
// display name as a unique key: names may be shared by facts of different kinds.
func buildGraphView(store *facts.Store, snapshotID string) *graphView {
	if store == nil {
		return nil
	}
	all := store.All()
	view := &graphView{SnapshotID: snapshotID, FactCount: len(all)}
	if len(all) == 0 {
		return view
	}

	ids := make([]string, len(all))
	byName := make(map[string][]int)
	for i, f := range all {
		ids[i] = factID(f, i)
		byName[f.Name] = append(byName[f.Name], i)
	}

	nodeKinds := map[string]bool{}
	edgeKinds := map[string]bool{}
	for i, f := range all {
		n := graphNode{
			ID: ids[i], Name: f.Name, Kind: f.Kind, Repo: f.Repo,
			Label: f.Name, File: f.File, Line: f.Line, Count: 1, Props: f.Props,
		}
		if n.Kind == "" {
			n.Kind = "unknown"
		}
		n.Conflated = conflatedKinds(byName[f.Name], all)
		nodeKinds[n.Kind] = true
		view.Nodes = append(view.Nodes, n)
	}

	// Resolve relation targets using the same natural target kinds as the
	// internal graph. If no fact backs a target, create one explicit unresolved
	// node rather than silently dropping the edge.
	unresolvedIDs := map[string]string{}
	for i, f := range all {
		for j, rel := range f.Relations {
			target := chooseFact(byName[rel.Target], all, relationTargetKind(rel.Kind))
			targetID := ""
			if target >= 0 {
				targetID = ids[target]
			} else {
				key := f.Repo + "\x00" + rel.Target
				targetID = unresolvedIDs[key]
				if targetID == "" {
					targetID = "unresolved-" + shortHash(key)
					unresolvedIDs[key] = targetID
					view.Nodes = append(view.Nodes, graphNode{
						ID: targetID, Name: rel.Target, Label: rel.Target,
						Kind: "unresolved", Repo: f.Repo, Count: 1, Unresolved: true,
					})
					view.Unresolved++
				}
			}
			edge := graphEdge{
				ID:     "edge-" + shortHash(ids[i]+"\x00"+rel.Kind+"\x00"+targetID+"\x00"+strconv.Itoa(j)),
				Source: ids[i], Target: targetID, Kind: rel.Kind, Count: 1,
			}
			view.Edges = append(view.Edges, edge)
			edgeKinds[rel.Kind] = true
		}
	}
	view.EdgeCount = len(view.Edges)

	view.OverviewNodes, view.OverviewEdges = overviewGraph(store, all, byName)
	view.NodeKinds = orderedNodeKinds(nodeKinds)
	view.EdgeKinds = sortedKeys(edgeKinds)
	sort.Slice(view.Nodes, func(i, j int) bool { return view.Nodes[i].ID < view.Nodes[j].ID })
	sort.Slice(view.Edges, func(i, j int) bool { return view.Edges[i].ID < view.Edges[j].ID })
	return view
}

// buildFocusedGraph converts a bounded graph traversal into the browser
// contract. The traversal is intentionally capped before serialization so a
// zoom or focus event cannot accidentally send the whole repository to the
// browser.
func buildFocusedGraph(store *facts.Store, snapshotID, focus, kind, repo, direction string, depth, maxNodes int) *focusedGraph {
	result := &focusedGraph{SnapshotID: snapshotID, Scope: "symbols", Focus: focus}
	if store == nil || store.Graph() == nil || focus == "" {
		return result
	}
	resolved := resolveFocus(store, focus, kind, repo)
	if resolved == "" {
		return result
	}
	if depth <= 0 {
		depth = 1
	}
	if maxNodes <= 0 {
		maxNodes = 150
	}
	if maxNodes > 300 {
		maxNodes = 300
	}
	nodeKinds := focusedNodeKinds()
	traversals := make([]facts.TraversalResult, 0, 2)
	if direction == "reverse" || direction == "both" {
		traversals = append(traversals, store.Graph().Traverse(resolved, "reverse", nil, nodeKinds, depth, maxNodes))
	}
	if direction == "forward" || direction == "both" || direction == "" {
		traversals = append(traversals, store.Graph().Traverse(resolved, "forward", nil, nodeKinds, depth, maxNodes))
	}

	nodeByName := map[string]graphNode{}
	edgeByKey := map[string]graphEdge{}
	for _, traversal := range traversals {
		result.Truncated = result.Truncated || traversal.Stats.Truncated
		for _, n := range traversal.Nodes {
			if _, exists := nodeByName[n.Name]; exists {
				continue
			}
			nodeByName[n.Name] = graphNode{
				ID: focusedNodeID(n), Name: n.Name, Label: n.Name,
				Kind: n.Kind, Repo: n.Repo, File: n.File, Line: n.Line,
				Count: 1, Unresolved: n.Unresolved, Conflated: n.Conflated,
			}
		}
		for _, e := range traversal.Edges {
			source, sourceOK := nodeByName[e.Source]
			target, targetOK := nodeByName[e.Target]
			if !sourceOK || !targetOK {
				continue
			}
			key := source.ID + "\x00" + e.Kind + "\x00" + target.ID
			edge := edgeByKey[key]
			if edge.ID == "" {
				edge = graphEdge{
					ID:     "focused-edge-" + shortHash(key),
					Source: source.ID, Target: target.ID, Kind: e.Kind, Count: 0,
				}
			}
			edge.Count++
			edgeByKey[key] = edge
		}
	}
	addFocusedImplements(store, nodeByName, edgeByKey, maxNodes)
	for _, n := range nodeByName {
		result.Nodes = append(result.Nodes, n)
	}
	for _, e := range edgeByKey {
		result.Edges = append(result.Edges, e)
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	result.NodeCount = len(result.Nodes)
	result.EdgeCount = len(result.Edges)
	result.HasMore = result.Truncated
	return result
}

// addFocusedImplements preserves the direct inheritance edges of symbols that
// made it into a bounded scope. A module traversal reaches its member symbols
// through their declares edges, but their base types are one relation further
// away; requiring the traversal to expand another hop would pull in unrelated
// neighborhoods. Add only those direct implements targets instead.
func addFocusedImplements(store *facts.Store, nodeByName map[string]graphNode, edgeByKey map[string]graphEdge, maxNodes int) {
	if store == nil {
		return
	}
	sources := make([]graphNode, 0, len(nodeByName))
	for _, node := range nodeByName {
		if node.Kind == facts.KindSymbol {
			sources = append(sources, node)
		}
	}
	for _, source := range sources {
		for _, fact := range store.LookupByExactName(source.Name) {
			if fact.Kind != facts.KindSymbol {
				continue
			}
			for _, rel := range fact.Relations {
				if rel.Kind != facts.RelImplements {
					continue
				}
				target, exists := nodeByName[rel.Target]
				if !exists {
					backing := chooseFocusedFact(store.LookupByExactName(rel.Target), facts.KindSymbol)
					if backing != nil {
						target = graphNode{
							ID: focusedNodeID(facts.TraversalNode{
								Name: backing.Name, Kind: backing.Kind, Repo: backing.Repo,
								File: backing.File, Line: backing.Line,
							}),
							Name: backing.Name, Label: backing.Name, Kind: backing.Kind,
							Repo: backing.Repo, File: backing.File, Line: backing.Line, Count: 1,
						}
					} else {
						target = graphNode{
							ID: "focused-node-" + shortHash(source.Repo + "\x00" + rel.Target),
							Name: rel.Target, Label: rel.Target, Kind: "unresolved",
							Repo: source.Repo, Count: 1, Unresolved: true,
						}
					}
					if maxNodes > 0 && len(nodeByName) >= maxNodes {
						continue
					}
					nodeByName[rel.Target] = target
				}
				key := source.ID + "\x00" + rel.Kind + "\x00" + target.ID
				edge := edgeByKey[key]
				if edge.ID == "" {
					edge = graphEdge{
						ID: "focused-edge-" + shortHash(key),
						Source: source.ID, Target: target.ID, Kind: rel.Kind, Count: 0,
					}
				}
				edge.Count++
				edgeByKey[key] = edge
			}
		}
	}
}

func chooseFocusedFact(candidates []facts.Fact, preferred string) *facts.Fact {
	for i := range candidates {
		if candidates[i].Kind == preferred {
			return &candidates[i]
		}
	}
	return nil
}

func resolveFocus(store *facts.Store, name, kind, repo string) string {
	for _, f := range store.LookupByExactName(name) {
		if (kind == "" || f.Kind == kind) && (repo == "" || f.Repo == repo) {
			return f.Name
		}
	}
	return ""
}

func focusedNodeKinds() []string {
	return []string{facts.KindModule, facts.KindService, facts.KindSymbol, facts.KindRoute, facts.KindStorage}
}

func focusedNodeID(n facts.TraversalNode) string {
	return "focused-node-" + shortHash(n.Repo+"\x00"+n.Kind+"\x00"+n.Name+"\x00"+n.File+"\x00"+strconv.Itoa(n.Line))
}

func overviewGraph(store *facts.Store, all []facts.Fact, byName map[string][]int) ([]graphNode, []graphEdge) {
	buckets := make([]string, len(all))
	nodes := map[string]graphNode{}
	for i, f := range all {
		bucket, kind, name := overviewBucket(f, all)
		if bucket == "" {
			continue
		}
		buckets[i] = bucket
		if _, exists := nodes[bucket]; !exists {
			nodes[bucket] = graphNode{
				ID: "overview-" + shortHash(bucket), Name: name, Label: name,
				Kind: kind, Repo: f.Repo, Count: 0,
			}
		}
		nodes[bucket] = incrementOverviewNode(nodes[bucket])
	}

	edges := map[string]graphEdge{}
	bucketsByName := map[string]map[string]bool{}
	for i, f := range all {
		if buckets[i] != "" {
			if bucketsByName[f.Name] == nil {
				bucketsByName[f.Name] = map[string]bool{}
			}
			bucketsByName[f.Name][buckets[i]] = true
		}
	}
	if store != nil && store.Graph() != nil {
		// Use the graph index rather than only raw fact relations. The index
		// includes normalized cross-repo calls and synthetic edges, so calls
		// from hidden members still roll up to their containing modules.
		for source, relations := range store.Graph().Forward() {
			for _, rel := range relations {
				for sourceBucket := range bucketsByName[source] {
					for targetBucket := range bucketsByName[rel.Target] {
						if sourceBucket == targetBucket {
							continue
						}
						sourceID := nodes[sourceBucket].ID
						targetID := nodes[targetBucket].ID
						key := sourceID + "\x00" + rel.RelKind + "\x00" + targetID
						edge := edges[key]
						if edge.ID == "" {
							edge = graphEdge{
								ID: "overview-edge-" + shortHash(key), Source: sourceID,
								Target: targetID, Kind: rel.RelKind, Count: 0, Aggregated: true,
							}
						}
						edge.Count++
						edges[key] = edge
					}
				}
			}
		}
	} else {
		// Store graphs are normally built before dashboard rendering. Keep a
		// direct-relation fallback for lightweight test fakes and empty stores.
		for i, f := range all {
			if buckets[i] == "" {
				continue
			}
			for _, rel := range f.Relations {
				target := chooseFact(byName[rel.Target], all, relationTargetKind(rel.Kind))
				if target < 0 || buckets[target] == "" || buckets[target] == buckets[i] {
					continue
				}
				sourceID := nodes[buckets[i]].ID
				targetID := nodes[buckets[target]].ID
				key := sourceID + "\x00" + rel.Kind + "\x00" + targetID
				edge := edges[key]
				if edge.ID == "" {
					edge = graphEdge{
						ID: "overview-edge-" + shortHash(key), Source: sourceID,
						Target: targetID, Kind: rel.Kind, Count: 0, Aggregated: true,
					}
				}
				edge.Count++
				edges[key] = edge
			}
		}
	}
	nodeList := make([]graphNode, 0, len(nodes))
	for _, n := range nodes {
		n.Label = n.Name + " (" + strconv.Itoa(n.Count) + ")"
		nodeList = append(nodeList, n)
	}
	sort.Slice(nodeList, func(i, j int) bool { return nodeList[i].ID < nodeList[j].ID })
	edgeList := make([]graphEdge, 0, len(edges))
	for _, e := range edges {
		edgeList = append(edgeList, e)
	}
	sort.Slice(edgeList, func(i, j int) bool { return edgeList[i].ID < edgeList[j].ID })
	return nodeList, edgeList
}

func orderedNodeKinds(kinds map[string]bool) []string {
	preferred := []string{"module", "symbol", "dependency", "file_ref"}
	ordered := make([]string, 0, len(kinds))
	seen := make(map[string]bool, len(preferred))
	for _, kind := range preferred {
		if kinds[kind] {
			ordered = append(ordered, kind)
			seen[kind] = true
		}
	}
	remaining := make(map[string]bool)
	for kind := range kinds {
		if !seen[kind] {
			remaining[kind] = true
		}
	}
	return append(ordered, sortedKeys(remaining)...)
}

func incrementOverviewNode(node graphNode) graphNode {
	node.Count++
	return node
}

func overviewBucket(f facts.Fact, all []facts.Fact) (bucket, kind, name string) {
	if f.Kind == facts.KindService {
		return "service\x00" + f.Repo + "\x00" + f.Name, facts.KindService, f.Name
	}
	if f.Kind == facts.KindModule {
		return "module\x00" + f.Repo + "\x00" + f.Name, facts.KindModule, f.Name
	}
	if f.File != "" {
		// Extractors are not uniform about whether repository names are part of
		// fact paths. Try the original path first because Python package/module
		// facts commonly retain the repository prefix (for example
		// "api/domain/booking"), then try the repo-relative path used by other
		// extractors.
		files := []string{f.File}
		if relative := strings.TrimPrefix(f.File, f.Repo+"/"); relative != f.File {
			files = append(files, relative)
		}
		for _, file := range files {
			dir := path.Dir(file)
			for {
				for _, candidate := range all {
					if candidate.Kind == facts.KindModule && candidate.Repo == f.Repo && candidate.Name == dir {
						return "module\x00" + f.Repo + "\x00" + candidate.Name, facts.KindModule, candidate.Name
					}
				}
				parent := path.Dir(dir)
				if parent == dir || dir == "." {
					break
				}
				dir = parent
			}
		}
	}
	if f.Repo != "" {
		return "service\x00" + f.Repo, facts.KindService, f.Repo
	}
	return "", "", ""
}

func relationTargetKind(kind string) string {
	switch kind {
	case facts.RelDependsOn:
		return facts.KindService
	case facts.RelImports:
		return facts.KindModule
	case facts.RelHandledBy:
		return facts.KindRoute
	default:
		return facts.KindSymbol
	}
}

func chooseFact(indices []int, all []facts.Fact, preferred string) int {
	if len(indices) == 0 {
		return -1
	}
	for _, i := range indices {
		if all[i].Kind == preferred {
			return i
		}
	}
	return indices[0]
}

func conflatedKinds(indices []int, all []facts.Fact) []string {
	kinds := map[string]bool{}
	for _, i := range indices {
		kinds[all[i].Kind] = true
	}
	if len(kinds) < 2 {
		return nil
	}
	return sortedKeys(kinds)
}

func factID(f facts.Fact, index int) string {
	key := f.Repo + "\x00" + f.Kind + "\x00" + f.Name + "\x00" + f.File + "\x00" + strconv.Itoa(f.Line) + "\x00" + strconv.Itoa(index)
	return "node-" + shortHash(key)
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (v graphView) JSON() string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func (v graphView) BootstrapJSON() string {
	b, err := json.Marshal(graphBootstrap{
		SnapshotID: v.SnapshotID, OverviewNodes: v.OverviewNodes,
		OverviewEdges: v.OverviewEdges, NodeKinds: v.NodeKinds,
		EdgeKinds: v.EdgeKinds, FactCount: v.FactCount,
		EdgeCount: v.EdgeCount, Unresolved: v.Unresolved,
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}
