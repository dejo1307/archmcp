// Package diff computes the delta between two architecture snapshots.
//
// Unlike a linter, which judges a snapshot against an external ideal, a diff
// judges the current snapshot against the codebase's OWN prior state. That makes
// it a ratchet rather than a ruler: it reports only what CHANGED — facts added or
// removed, new coupling edges, findings that newly appeared or were resolved —
// and stays silent about pre-existing state. A pattern that was "wrong" before and
// after (e.g. an API-first route with no loaded consumer) produces no delta, so
// the diff is structurally immune to the false-signal problem.
//
// Compute is pure and deterministic: identical inputs always yield byte-identical
// output (every collection is sorted by a stable key), so a diff is reproducible
// and even diffs-of-diffs are stable.
package diff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// Edge is a directed relation between two facts, identified at the
// name level (not file/line) so that moving a symbol between files does not churn
// its edges — what matters for coupling is "X depends on Y", not where X lives.
type Edge struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Repo   string `json:"repo,omitempty"`
}

// FactChange records a fact present in both snapshots whose own attributes
// (props such as signature, exported, cyclomatic) changed. Line-only shifts are
// deliberately NOT treated as changes — an edit above a symbol moves every symbol
// below it, which would flood the diff with noise that says nothing architectural.
type FactChange struct {
	Before facts.Fact `json:"before"`
	After  facts.Fact `json:"after"`
}

// SnapshotDiff is the delta between a baseline and a current snapshot.
type SnapshotDiff struct {
	BaselineRepo        string `json:"baseline_repo,omitempty"`
	CurrentRepo         string `json:"current_repo,omitempty"`
	BaselineGeneratedAt string `json:"baseline_generated_at,omitempty"`
	CurrentGeneratedAt  string `json:"current_generated_at,omitempty"`

	// Structural changes.
	FactsAdded   []facts.Fact `json:"facts_added,omitempty"`
	FactsRemoved []facts.Fact `json:"facts_removed,omitempty"`
	FactsChanged []FactChange `json:"facts_changed,omitempty"`
	EdgesAdded   []Edge       `json:"edges_added,omitempty"`
	EdgesRemoved []Edge       `json:"edges_removed,omitempty"`

	// Findings delta (the ratchet core). FindingsNew are regressions introduced
	// by the change; FindingsResolved are issues the change cleared. Each carries
	// through its original Confidence and Description (caveats intact) untouched —
	// the diff manufactures no verdicts.
	FindingsNew      []facts.Insight `json:"findings_new,omitempty"`
	FindingsResolved []facts.Insight `json:"findings_resolved,omitempty"`
}

// Compute returns the delta from baseline to current. A nil snapshot is treated
// as empty (so the first diff against no baseline reports everything as added).
func Compute(baseline, current *facts.Snapshot) *SnapshotDiff {
	d := &SnapshotDiff{}
	if baseline != nil {
		d.BaselineRepo = baseline.Meta.RepoPath
		d.BaselineGeneratedAt = baseline.Meta.GeneratedAt
	}
	if current != nil {
		d.CurrentRepo = current.Meta.RepoPath
		d.CurrentGeneratedAt = current.Meta.GeneratedAt
	}

	baseFacts := snapFacts(baseline)
	curFacts := snapFacts(current)

	baseByKey := make(map[string]facts.Fact, len(baseFacts))
	for _, f := range baseFacts {
		baseByKey[factKey(f)] = f
	}
	curByKey := make(map[string]facts.Fact, len(curFacts))
	for _, f := range curFacts {
		curByKey[factKey(f)] = f
	}

	for k, cf := range curByKey {
		bf, ok := baseByKey[k]
		if !ok {
			d.FactsAdded = append(d.FactsAdded, cf)
			continue
		}
		if propsChanged(bf, cf) {
			d.FactsChanged = append(d.FactsChanged, FactChange{Before: bf, After: cf})
		}
	}
	for k, bf := range baseByKey {
		if _, ok := curByKey[k]; !ok {
			d.FactsRemoved = append(d.FactsRemoved, bf)
		}
	}

	baseEdges := edgeSet(baseFacts)
	curEdges := edgeSet(curFacts)
	for k, e := range curEdges {
		if _, ok := baseEdges[k]; !ok {
			d.EdgesAdded = append(d.EdgesAdded, e)
		}
	}
	for k, e := range baseEdges {
		if _, ok := curEdges[k]; !ok {
			d.EdgesRemoved = append(d.EdgesRemoved, e)
		}
	}

	baseFind := make(map[string]facts.Insight, len(snapInsights(baseline)))
	for _, in := range snapInsights(baseline) {
		baseFind[findingKey(in)] = in
	}
	curFind := make(map[string]facts.Insight)
	for _, in := range snapInsights(current) {
		curFind[findingKey(in)] = in
	}
	for k, in := range curFind {
		if _, ok := baseFind[k]; !ok {
			d.FindingsNew = append(d.FindingsNew, in)
		}
	}
	for k, in := range baseFind {
		if _, ok := curFind[k]; !ok {
			d.FindingsResolved = append(d.FindingsResolved, in)
		}
	}

	d.sortAll()
	return d
}

// Empty reports whether the diff contains no changes of any kind.
func (d *SnapshotDiff) Empty() bool {
	return len(d.FactsAdded) == 0 && len(d.FactsRemoved) == 0 && len(d.FactsChanged) == 0 &&
		len(d.EdgesAdded) == 0 && len(d.EdgesRemoved) == 0 &&
		len(d.FindingsNew) == 0 && len(d.FindingsResolved) == 0
}

// Focused returns a copy of the diff narrowed to entries that reference focus
// (case-insensitive substring against fact name/file, edge source/target, and
// finding title/evidence). An empty focus returns the diff unchanged. This lets
// an agent verify just the area it touched.
func (d *SnapshotDiff) Focused(focus string) *SnapshotDiff {
	focus = strings.ToLower(strings.TrimSpace(focus))
	if focus == "" {
		return d
	}
	out := &SnapshotDiff{
		BaselineRepo:        d.BaselineRepo,
		CurrentRepo:         d.CurrentRepo,
		BaselineGeneratedAt: d.BaselineGeneratedAt,
		CurrentGeneratedAt:  d.CurrentGeneratedAt,
	}
	for _, f := range d.FactsAdded {
		if factMatches(f, focus) {
			out.FactsAdded = append(out.FactsAdded, f)
		}
	}
	for _, f := range d.FactsRemoved {
		if factMatches(f, focus) {
			out.FactsRemoved = append(out.FactsRemoved, f)
		}
	}
	for _, c := range d.FactsChanged {
		if factMatches(c.After, focus) || factMatches(c.Before, focus) {
			out.FactsChanged = append(out.FactsChanged, c)
		}
	}
	for _, e := range d.EdgesAdded {
		if edgeMatches(e, focus) {
			out.EdgesAdded = append(out.EdgesAdded, e)
		}
	}
	for _, e := range d.EdgesRemoved {
		if edgeMatches(e, focus) {
			out.EdgesRemoved = append(out.EdgesRemoved, e)
		}
	}
	for _, in := range d.FindingsNew {
		if insightMatches(in, focus) {
			out.FindingsNew = append(out.FindingsNew, in)
		}
	}
	for _, in := range d.FindingsResolved {
		if insightMatches(in, focus) {
			out.FindingsResolved = append(out.FindingsResolved, in)
		}
	}
	return out
}

// --- identity keys ---

// factKey identifies a fact by (kind, repo, file, name) plus a kind-specific
// discriminator. File is included so a symbol moved to another file shows as
// remove+add (acceptable for v1; the agent that moved it knows it did). Line is
// intentionally excluded — it is not identity, so a line shift never churns the diff.
//
// The discriminator exists because (kind, repo, file, name) is NOT unique for
// every kind: the same DB table is referenced by several SQL operations in one
// file, and the same route path is served under multiple HTTP methods. Without it
// those distinct facts collapse to one key, and the diff falsely reports the
// survivor as "changed" run-to-run — the colliding facts' map-iteration
// representative differs between an on-disk baseline and the in-memory current.
func factKey(f facts.Fact) string {
	return f.Kind + "\x00" + f.Repo + "\x00" + f.File + "\x00" + f.Name + "\x00" + factDiscriminator(f)
}

// factDiscriminator returns the props that distinguish facts which legitimately
// share (kind, repo, file, name). It is kind-specific because only a few kinds
// have multiple facts per name; for the rest the fully-qualified name is unique.
// It deliberately uses identity-bearing props (a route's method, a storage
// reference's operation), not mutable ones, so a genuine attribute change still
// surfaces as "changed" rather than remove+add.
func factDiscriminator(f facts.Fact) string {
	switch f.Kind {
	case facts.KindRoute:
		return propString(f.Props, "method")
	case facts.KindStorage:
		return propString(f.Props, "operation") + "|" + propString(f.Props, "storage_kind")
	default:
		return ""
	}
}

// propString returns the named prop as a string, or "" if absent.
func propString(props map[string]any, key string) string {
	if props == nil {
		return ""
	}
	if v, ok := props[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func edgeKey(e Edge) string {
	return e.Repo + "\x00" + e.Source + "\x00" + e.Kind + "\x00" + e.Target
}

// findingKey identifies an insight by its explainer plus the sorted set of
// entities it cites (evidence Fact/Symbol/File). Title and Detail are excluded
// because they often embed volatile metrics (e.g. "fan-in: 13"); keying on the
// entities keeps a finding stable across runs so only a finding about a NEW entity
// counts as new. Evidence-less insights fall back to their title.
func findingKey(in facts.Insight) string {
	var ents []string
	for _, ev := range in.Evidence {
		if e := firstNonEmpty(ev.Fact, ev.Symbol, ev.File); e != "" {
			ents = append(ents, e)
		}
	}
	if len(ents) == 0 {
		return in.Source + "\x00" + in.Title
	}
	sort.Strings(ents)
	return in.Source + "\x00" + strings.Join(ents, "\x1f")
}

// --- helpers ---

func snapFacts(s *facts.Snapshot) []facts.Fact {
	if s == nil {
		return nil
	}
	return s.Facts
}

func snapInsights(s *facts.Snapshot) []facts.Insight {
	if s == nil {
		return nil
	}
	return s.Insights
}

// edgeSet returns the deduplicated set of edges across all facts, keyed by edgeKey.
func edgeSet(ff []facts.Fact) map[string]Edge {
	set := make(map[string]Edge)
	for _, f := range ff {
		for _, r := range f.Relations {
			e := Edge{Source: f.Name, Kind: r.Kind, Target: r.Target, Repo: f.Repo}
			set[edgeKey(e)] = e
		}
	}
	return set
}

// propsChanged reports whether two facts sharing an identity differ in their
// props. encoding/json sorts map keys, so the marshaled form is order-stable.
func propsChanged(a, b facts.Fact) bool {
	if len(a.Props) == 0 && len(b.Props) == 0 {
		return false
	}
	return propsJSON(a.Props) != propsJSON(b.Props)
}

func propsJSON(p map[string]any) string {
	if len(p) == 0 {
		return ""
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func factMatches(f facts.Fact, focusLC string) bool {
	return strings.Contains(strings.ToLower(f.Name), focusLC) ||
		strings.Contains(strings.ToLower(f.File), focusLC)
}

func edgeMatches(e Edge, focusLC string) bool {
	return strings.Contains(strings.ToLower(e.Source), focusLC) ||
		strings.Contains(strings.ToLower(e.Target), focusLC)
}

func insightMatches(in facts.Insight, focusLC string) bool {
	if strings.Contains(strings.ToLower(in.Title), focusLC) {
		return true
	}
	for _, ev := range in.Evidence {
		if strings.Contains(strings.ToLower(ev.Fact), focusLC) ||
			strings.Contains(strings.ToLower(ev.Symbol), focusLC) ||
			strings.Contains(strings.ToLower(ev.File), focusLC) {
			return true
		}
	}
	return false
}

// sortAll orders every collection by its stable key so output is deterministic.
func (d *SnapshotDiff) sortAll() {
	sort.Slice(d.FactsAdded, func(i, j int) bool { return factKey(d.FactsAdded[i]) < factKey(d.FactsAdded[j]) })
	sort.Slice(d.FactsRemoved, func(i, j int) bool { return factKey(d.FactsRemoved[i]) < factKey(d.FactsRemoved[j]) })
	sort.Slice(d.FactsChanged, func(i, j int) bool {
		return factKey(d.FactsChanged[i].After) < factKey(d.FactsChanged[j].After)
	})
	sort.Slice(d.EdgesAdded, func(i, j int) bool { return edgeKey(d.EdgesAdded[i]) < edgeKey(d.EdgesAdded[j]) })
	sort.Slice(d.EdgesRemoved, func(i, j int) bool { return edgeKey(d.EdgesRemoved[i]) < edgeKey(d.EdgesRemoved[j]) })
	sort.Slice(d.FindingsNew, func(i, j int) bool { return findingKey(d.FindingsNew[i]) < findingKey(d.FindingsNew[j]) })
	sort.Slice(d.FindingsResolved, func(i, j int) bool {
		return findingKey(d.FindingsResolved[i]) < findingKey(d.FindingsResolved[j])
	})
}

// KindCounts returns counts of facts by kind for the given slice, used by the
// renderer's structural-change summary.
func KindCounts(ff []facts.Fact) map[string]int {
	m := make(map[string]int)
	for _, f := range ff {
		m[f.Kind]++
	}
	return m
}
