// Package explain produces a human-readable statistical summary of an Enola
// architectural snapshot — the data behind `enola --explain <repository>`.
//
// It is intentionally a public package (not internal/) so that enola-enterprise
// can reuse the base Report and append its own license-gated sections (dead code,
// package metrics) before rendering. Compute works purely off the exported
// bootstrap.Engine API, so it sees whatever the engine currently holds: run
// GenerateSnapshot (or auto-load a snapshot) first.
package explain

import (
	"sort"
	"strconv"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/bootstrap"
)

// Criticality thresholds for a module hotspot, scored by fan-in + fan-out.
// Mirrors the llm_context renderer so "critical module" means the same thing
// everywhere.
const (
	criticalHigh   = 10
	criticalMedium = 5
)

// blastDepth / blastNodes bound the reverse reachability used to estimate a
// hotspot's blast radius (the impact_analysis number). Kept modest so --explain
// stays fast even on large repos; the total is still accurate within the depth.
const (
	blastDepth  = 3
	blastNodes  = 500
	topHotspots = 8
)

// LabelCount is a named tally (a kind, a symbol kind, an HTTP method, …).
type LabelCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Hotspot is a module ranked by coupling, with its estimated change blast radius.
type Hotspot struct {
	Module      string `json:"module"`
	FanIn       int    `json:"fan_in"`
	FanOut      int    `json:"fan_out"`
	Criticality string `json:"criticality"`  // high | medium | low
	BlastRadius int    `json:"blast_radius"` // transitive reverse-dependents within blastDepth
}

// Section is an extra block appended to the report by enterprise code. Body is
// pre-rendered text (the lines under the Title heading).
type Section struct {
	Title string
	Body  string
}

// Report is the full statistical picture of a snapshot. Fields are plain types
// only, so consumers in other modules (enola-enterprise) can read them without
// importing enola's internal packages.
type Report struct {
	RepoPath    string   `json:"repo_path"`
	GeneratedAt string   `json:"generated_at,omitempty"`
	Duration    string   `json:"duration,omitempty"`
	Extractors  []string `json:"extractors,omitempty"`
	TotalFacts  int      `json:"total_facts"`

	KindCounts  []LabelCount `json:"kind_counts"`  // module/symbol/route/storage/dependency/service
	SymbolKinds []LabelCount `json:"symbol_kinds"` // function/method/struct/…
	DepSources  []LabelCount `json:"dep_sources"`  // external/internal/stdlib/…

	Routes         int          `json:"routes"`
	RoutesByMethod []LabelCount `json:"routes_by_method,omitempty"`
	Storage        int          `json:"storage"`

	Architecture    string  `json:"architecture,omitempty"`
	ArchConfidence  float64 `json:"architecture_confidence,omitempty"`
	Cycles          int     `json:"cyclic_dependencies"`
	LayerViolations int     `json:"layer_violations"`
	CrossRepoEdges  int     `json:"cross_repo_edges"`

	Modules           int       `json:"modules"`
	HighCriticality   int       `json:"high_criticality"`
	MediumCriticality int       `json:"medium_criticality"`
	Hotspots          []Hotspot `json:"hotspots,omitempty"`

	// CouplingUnresolved is true when dependency facts exist but none of their
	// import edges resolved to a module — coupling analysis is unavailable, not
	// genuinely zero. The renderer surfaces this as a note.
	CouplingUnresolved bool `json:"coupling_unresolved,omitempty"`

	// ExtraSections are appended (e.g. by enterprise) and rendered after the
	// base report.
	ExtraSections []Section `json:"-"`
}

// Compute reads the engine's current fact store and snapshot and builds a Report.
// It does not generate a snapshot — callers do that first.
func Compute(eng *bootstrap.Engine) *Report {
	store := eng.Store()
	snap := eng.Snapshot()

	r := &Report{TotalFacts: store.Count()}
	if snap != nil {
		r.RepoPath = snap.Meta.RepoPath
		r.GeneratedAt = snap.Meta.GeneratedAt
		r.Duration = snap.Meta.Duration
		r.Extractors = snap.Meta.Extractors
	}

	// Architectural-kind tallies, in the canonical order from ARCHITECTURE.md.
	for _, k := range []string{
		facts.KindModule, facts.KindSymbol, facts.KindRoute,
		facts.KindStorage, facts.KindDependency, facts.KindService,
	} {
		if n := len(store.ByKind(k)); n > 0 {
			r.KindCounts = append(r.KindCounts, LabelCount{Label: k, Count: n})
		}
	}

	// Symbol-kind breakdown (function/method/struct/…).
	skCount := map[string]int{}
	for _, f := range store.ByKind(facts.KindSymbol) {
		sk, _ := f.Props["symbol_kind"].(string)
		if sk == "" {
			sk = "unknown"
		}
		skCount[sk]++
	}
	r.SymbolKinds = sortedCounts(skCount)

	// Routes, broken down by HTTP method.
	routes := store.ByKind(facts.KindRoute)
	r.Routes = len(routes)
	methodCount := map[string]int{}
	for _, f := range routes {
		m, _ := f.Props["method"].(string)
		if m == "" {
			m = "(unspecified)"
		} else {
			m = strings.ToUpper(m)
		}
		methodCount[m]++
	}
	if r.Routes > 0 {
		r.RoutesByMethod = sortedCounts(methodCount)
	}

	r.Storage = len(store.ByKind(facts.KindStorage))

	// Dependency facts grouped by their declared source (external/internal/stdlib).
	srcCount := map[string]int{}
	for _, f := range store.ByKind(facts.KindDependency) {
		s, _ := f.Props["source"].(string)
		if s == "" {
			s = "unclassified"
		}
		srcCount[s]++
	}
	if len(srcCount) > 0 {
		r.DepSources = sortedCounts(srcCount)
	}

	r.Modules = len(store.ByKind(facts.KindModule))

	// Insight-derived numbers: architecture pattern, cycles, layer violations,
	// cross-repo edges. Titles are matched against the explainer formats.
	if snap != nil {
		for _, in := range snap.Insights {
			switch {
			case strings.HasPrefix(in.Title, "Cyclic dependency"):
				r.Cycles++
			case strings.HasPrefix(in.Title, "Layer violation"):
				r.LayerViolations++
			case strings.HasPrefix(in.Title, "Architecture pattern:"):
				r.Architecture = strings.TrimSpace(strings.TrimPrefix(in.Title, "Architecture pattern:"))
				r.ArchConfidence = in.Confidence
			case strings.HasPrefix(in.Title, "Cross-repo dependencies"):
				r.CrossRepoEdges = firstParenInt(in.Title)
			}
		}
	}

	computeHotspots(store, r)
	return r
}

// computeHotspots ranks modules by fan-in + fan-out (the same coupling signal as
// the llm_context "Critical Modules" table) and estimates each top module's
// change blast radius via reverse graph reachability.
func computeHotspots(store *facts.Store, r *Report) {
	modules := map[string]bool{}
	for _, f := range store.ByKind(facts.KindModule) {
		modules[f.Name] = true
	}

	fanIn := map[string]int{}
	fanOut := map[string]int{}
	resolvedEdges := 0
	deps := store.ByKind(facts.KindDependency)
	for _, dep := range deps {
		src := fileDir(dep.File)
		for _, rel := range dep.Relations {
			if rel.Kind != facts.RelImports {
				continue
			}
			// Resolve the import target to its nearest enclosing module. Some
			// extractors (e.g. Kotlin) emit type-level targets one segment below the
			// module dir; graph.go and the package-metrics tool already walk up, so
			// resolve here too rather than requiring an exact module match. External
			// targets are dotted (no '/'), so the walk-up finds nothing and they are
			// correctly ignored.
			if dst := resolveToModule(rel.Target, modules); dst != "" {
				fanOut[src]++
				fanIn[dst]++
				resolvedEdges++
			}
		}
	}

	// Dependency facts exist but nothing resolved to a module: coupling could not
	// be computed (e.g. an extractor whose import targets don't match module
	// names). Flag it so the renderer says so rather than implying zero coupling.
	if len(deps) > 0 && resolvedEdges == 0 {
		r.CouplingUnresolved = true
	}

	type scored struct {
		name          string
		fanIn, fanOut int
		score         int
	}
	var ranked []scored
	for mod := range modules {
		s := scored{name: mod, fanIn: fanIn[mod], fanOut: fanOut[mod], score: fanIn[mod] + fanOut[mod]}
		if s.score == 0 {
			continue
		}
		switch {
		case s.score >= criticalHigh:
			r.HighCriticality++
		case s.score >= criticalMedium:
			r.MediumCriticality++
		}
		ranked = append(ranked, s)
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].name < ranked[j].name // stable, deterministic
	})

	graph := store.Graph()
	limit := topHotspots
	if len(ranked) < limit {
		limit = len(ranked)
	}
	for _, s := range ranked[:limit] {
		h := Hotspot{
			Module:      s.name,
			FanIn:       s.fanIn,
			FanOut:      s.fanOut,
			Criticality: criticalityLabel(s.score),
		}
		if graph != nil {
			h.BlastRadius = graph.ImpactSet(s.name, blastDepth, blastNodes, false).TotalDependents
		}
		r.Hotspots = append(r.Hotspots, h)
	}
}

func criticalityLabel(score int) string {
	switch {
	case score >= criticalHigh:
		return "high"
	case score >= criticalMedium:
		return "medium"
	default:
		return "low"
	}
}

// sortedCounts converts a tally map into a slice ordered by count desc, then
// label asc, for deterministic output.
func sortedCounts(m map[string]int) []LabelCount {
	out := make([]LabelCount, 0, len(m))
	for k, v := range m {
		out = append(out, LabelCount{Label: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// firstParenInt extracts the first integer appearing inside parentheses, e.g.
// "Cross-repo dependencies (7 edges)" -> 7. Returns 0 if none is found.
func firstParenInt(s string) int {
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return 0
	}
	rest := s[open+1:]
	digits := strings.Builder{}
	for _, ch := range rest {
		if ch >= '0' && ch <= '9' {
			digits.WriteRune(ch)
		} else if digits.Len() > 0 {
			break
		}
	}
	if digits.Len() == 0 {
		return 0
	}
	n, _ := strconv.Atoi(digits.String())
	return n
}

// resolveToModule returns the nearest enclosing module of target: target itself if
// it is a module, else its closest ancestor directory that is. Returns "" if none.
// Mirrors graph.go's resolveToModule (unexported there), so hotspot coupling sees
// the same edges as traversal and package metrics.
func resolveToModule(target string, modules map[string]bool) string {
	cur := target
	for cur != "" {
		if modules[cur] {
			return cur
		}
		i := strings.LastIndex(cur, "/")
		if i < 0 {
			return ""
		}
		cur = cur[:i]
	}
	return ""
}

// fileDir returns the directory portion of a repo-relative file path (the module
// a fact belongs to). Mirrors the llm_context renderer.
func fileDir(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

// AddSection appends an extra section (used by enterprise code) and returns the
// report for chaining.
func (r *Report) AddSection(title, body string) *Report {
	r.ExtraSections = append(r.ExtraSections, Section{Title: title, Body: body})
	return r
}
