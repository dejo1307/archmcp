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
	"regexp"
	"sort"
	"strings"
	"time"

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

// InsightChange records a finding present in BOTH snapshots under the same identity
// whose content moved — its title metric, confidence, description or evidence.
//
// findingKey deliberately erases the numbers from a title (see normalizeTitle), so a
// finding stays the same finding as its metrics drift. That is right for identity and
// wrong as a conclusion: "the same finding" is not "an unchanged finding". Without this
// bucket the dead-code rollup could go from "5621 more" to "3662 more" — a 1,959-symbol
// swing — and land nowhere at all, while the diff printed "did not add, remove, or alter
// any … findings". The diff is the loop's own collateral-damage instrument, so silence
// is read as safety.
type InsightChange struct {
	Before facts.Insight `json:"before"`
	After  facts.Insight `json:"after"`
}

// Comparability records whether the baseline and current snapshots were
// generated over equivalent inputs. A diff is only trustworthy when they were:
// diffing a baseline taken with an extractor disabled against a run with it
// enabled would report every one of that language's facts as churn. Warnings
// here are surfaced ABOVE the delta so a mismatched comparison is flagged before
// its (misleading) numbers are read.
type Comparability struct {
	// Comparable is the explicit machine-readable verdict: true when no warnings
	// were raised (the two snapshots were generated over equivalent inputs). It is
	// always emitted so a JSON consumer reads the verdict directly rather than
	// inferring it from an empty warnings list. Invariant: Comparable == (len(Warnings) == 0).
	Comparable bool     `json:"comparable"`
	Warnings   []string `json:"warnings,omitempty"`
}

// SnapshotDiff is the delta between a baseline and a current snapshot.
type SnapshotDiff struct {
	BaselineRepo        string `json:"baseline_repo,omitempty"`
	CurrentRepo         string `json:"current_repo,omitempty"`
	BaselineGeneratedAt string `json:"baseline_generated_at,omitempty"`
	CurrentGeneratedAt  string `json:"current_generated_at,omitempty"`

	// Comparability guards the delta: warnings when baseline and current were not
	// generated over equivalent inputs (different extractor set, enola version,
	// ignore globs, or repo). Empty when the two are safely comparable.
	Comparability Comparability `json:"comparability,omitempty"`

	// Structural changes.
	FactsAdded   []facts.Fact `json:"facts_added,omitempty"`
	FactsRemoved []facts.Fact `json:"facts_removed,omitempty"`
	FactsChanged []FactChange `json:"facts_changed,omitempty"`
	EdgesAdded   []Edge       `json:"edges_added,omitempty"`
	EdgesRemoved []Edge       `json:"edges_removed,omitempty"`

	// Findings delta (the ratchet core). FindingsNew are regressions introduced
	// by the change; FindingsResolved are issues the change cleared. Each carries
	// through its original Confidence and Description (caveats intact) untouched —
	// the diff manufactures no verdicts. Only findings with a STRUCTURAL CAUSE in
	// this change (an evidence entity that was added/removed/changed) land here.
	FindingsNew      []facts.Insight `json:"findings_new,omitempty"`
	FindingsResolved []facts.Insight `json:"findings_resolved,omitempty"`

	// Incidental finding shifts: findings that appeared or cleared with NO
	// structural cause in this change — a moving statistical threshold (mean+2σ) or
	// a re-ranked top-N list whose membership shifted because some OTHER finding
	// left the window. These are surfaced separately so they don't masquerade as
	// regressions/improvements the change actually caused.
	FindingsNewIncidental      []facts.Insight `json:"findings_new_incidental,omitempty"`
	FindingsResolvedIncidental []facts.Insight `json:"findings_resolved_incidental,omitempty"`

	// FindingsChanged are findings that survived under the same identity but whose
	// content moved. Facts have had this since the beginning (FactsChanged); findings
	// never did, so a content-only change was reported as no change at all.
	FindingsChanged []InsightChange `json:"findings_changed,omitempty"`
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
	if baseline != nil && current != nil {
		d.Comparability = compareMeta(baseline.Meta, current.Meta)
	} else {
		// No baseline (or no current) to check against — the delta stands alone, so
		// there is nothing it could be incomparable with.
		d.Comparability = Comparability{Comparable: true}
	}

	baseFacts := snapFacts(baseline)
	curFacts := snapFacts(current)

	baseByKey := groupByKey(baseFacts)
	curByKey := groupByKey(curFacts)

	for k, curGroup := range curByKey {
		baseGroup := baseByKey[k]
		// Pair member i of the baseline group with member i of the current group.
		// Both groups are ordered by intraGroupOrder, so the pairing is stable
		// across the on-disk baseline and the in-memory current snapshot.
		for i, cf := range curGroup {
			if i >= len(baseGroup) {
				d.FactsAdded = append(d.FactsAdded, cf)
				continue
			}
			if propsChanged(baseGroup[i], cf) {
				d.FactsChanged = append(d.FactsChanged, FactChange{Before: baseGroup[i], After: cf})
			}
		}
	}
	for k, baseGroup := range baseByKey {
		curGroup := curByKey[k]
		for i := len(curGroup); i < len(baseGroup); i++ {
			d.FactsRemoved = append(d.FactsRemoved, baseGroup[i])
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

	// Grouped, not map[key]Insight. Findings collide: 78 of a large Android app's
	// layer violations share the title "Layer violation: di -> ui" and differ only in
	// their evidence, so a plain map kept ONE and silently dropped 77 — they could
	// appear or vanish and the diff would report nothing. Evidence cannot go into the
	// key (TestCompute_SummaryFindingDoesNotChurn forbids it), so group and pair
	// positionally, exactly as groupByKey already does for facts (fixed/10, where the
	// same collision hid 172 facts).
	baseFind := groupInsightsByKey(snapInsights(baseline))
	curFind := groupInsightsByKey(snapInsights(current))

	// A finding only counts as a real regression/improvement if this change
	// structurally touched something it cites — otherwise its appearance/clearance
	// is incidental (a moving mean+2σ threshold, or a top-N list re-ranking after
	// some other finding left the window). touched is the set of names the change
	// added/removed/altered, including edge endpoints (so a finding that flips
	// because a NEW caller changed a symbol's fan-in is still counted as real).
	touched := d.touchedNames()
	for k, curGroup := range curFind {
		baseGroup := baseFind[k]
		for i, in := range curGroup {
			if i >= len(baseGroup) {
				// Genuinely new: the current side has more findings under this identity.
				if findingHasStructuralCause(in, touched) {
					d.FindingsNew = append(d.FindingsNew, in)
				} else {
					d.FindingsNewIncidental = append(d.FindingsNewIncidental, in)
				}
				continue
			}
			// Present on both sides under the same identity. It is the SAME finding —
			// but that says nothing about whether it is UNCHANGED, which is the
			// conflation this bucket exists to undo.
			if insightChanged(baseGroup[i], in) {
				d.FindingsChanged = append(d.FindingsChanged, InsightChange{Before: baseGroup[i], After: in})
			}
		}
	}
	for k, baseGroup := range baseFind {
		curGroup := curFind[k]
		for i := len(curGroup); i < len(baseGroup); i++ {
			in := baseGroup[i]
			if findingHasStructuralCause(in, touched) {
				d.FindingsResolved = append(d.FindingsResolved, in)
			} else {
				d.FindingsResolvedIncidental = append(d.FindingsResolvedIncidental, in)
			}
		}
	}

	d.sortAll()
	return d
}

// compareMeta checks that two snapshots were generated over equivalent inputs and
// returns comparability warnings for each mismatch that would distort the delta.
// An auto-loaded baseline carries an empty Meta (only RepoPath) — its unknown
// extractor/version fields are treated as "cannot verify" (a soft note), never a
// hard mismatch, so the common single-repo verify loop is not spuriously warned.
func compareMeta(base, cur facts.SnapshotMeta) Comparability {
	var c Comparability

	if base.RepoPath != "" && cur.RepoPath != "" && base.RepoPath != cur.RepoPath {
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"baseline and current are different repositories (%s vs %s) — the delta is unlikely to be meaningful",
			base.RepoPath, cur.RepoPath))
	}

	if base.EnolaVersion == "" || cur.EnolaVersion == "" {
		c.Warnings = append(c.Warnings,
			"baseline predates snapshot receipts (no recorded enola version/extractors) — comparability cannot be fully verified; re-pin the baseline to silence this")
	} else if base.EnolaVersion != cur.EnolaVersion {
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"baseline was generated by a different enola version (%s vs %s) — extractor changes between versions can appear as spurious churn",
			base.EnolaVersion, cur.EnolaVersion))
	}

	// Extractors in the baseline but not the current run: the baseline had them and
	// the current lost them, so all of their facts appear as REMOVED.
	if only := missingFrom(base.Extractors, cur.Extractors); len(only) > 0 {
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"baseline had extractor(s) not in the current run: %s — their facts will all appear as REMOVED",
			strings.Join(only, ", ")))
	}
	// Extractors in the current run but not the baseline: the current gained them, so
	// all of their facts appear as ADDED.
	if only := missingFrom(cur.Extractors, base.Extractors); len(only) > 0 {
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"current run added extractor(s) not in the baseline: %s — their facts will all appear as ADDED",
			strings.Join(only, ", ")))
	}

	if base.IgnoreGlobHash != "" && cur.IgnoreGlobHash != "" && base.IgnoreGlobHash != cur.IgnoreGlobHash {
		c.Warnings = append(c.Warnings,
			"ignore globs differ between baseline and current — the set of files parsed changed, so some deltas may be exclusion changes rather than code changes")
	}

	// A `pinned` baseline persists on disk indefinitely — that is advertised as a
	// feature, so it stays valid across several edit rounds. The failure mode is that it
	// also stays valid across several WEEKS of unrelated repo drift, and nothing said so:
	// GeneratedAt was printed on line 3 and never compared. An 11-day-old baseline from
	// the same enola version with the same extractors yields Comparable:true, zero
	// warnings, and a confident 24-regression report for a change that touched no facts.
	if age, ok := baselineAgeDays(base.GeneratedAt, cur.GeneratedAt); ok && age >= staleBaselineDays {
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"baseline is %d days older than the current snapshot — anything the repo itself changed in "+
				"between will appear as part of this delta; re-pin the baseline (set_baseline) to compare only your change",
			age))
	}

	c.Comparable = len(c.Warnings) == 0
	return c
}

// missingFrom returns the elements of want that are absent from have, sorted.
// staleBaselineDays is how far apart two snapshots may be before the delta is more
// likely to describe the repo's own drift than the caller's change. Deliberately a
// warning, not a refusal: a long-lived baseline is a legitimate way to measure a
// multi-day refactor, and the caller is the only one who knows which they meant.
const staleBaselineDays = 3

// baselineAgeDays returns how many whole days older the baseline is than the current
// snapshot. Both timestamps are RFC3339 UTC (facts.SnapshotMeta.GeneratedAt); an
// unparseable or missing one yields ok=false, which is the pre-receipt baseline case
// that already has its own warning.
func baselineAgeDays(baseTS, curTS string) (int, bool) {
	b, err := time.Parse(time.RFC3339, baseTS)
	if err != nil {
		return 0, false
	}
	c, err := time.Parse(time.RFC3339, curTS)
	if err != nil {
		return 0, false
	}
	d := c.Sub(b)
	if d <= 0 {
		return 0, false
	}
	return int(d.Hours() / 24), true
}

func missingFrom(want, have []string) []string {
	set := make(map[string]struct{}, len(have))
	for _, h := range have {
		set[h] = struct{}{}
	}
	var out []string
	for _, w := range want {
		if _, ok := set[w]; !ok {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

// touchedNames is the set of entity names this change structurally affected:
// added/removed/changed facts plus the endpoints of added/removed edges. A
// finding is attributed to the change when one of its evidence entities is in
// this set.
func (d *SnapshotDiff) touchedNames() map[string]struct{} {
	m := make(map[string]struct{})
	add := func(n string) {
		if n != "" {
			m[n] = struct{}{}
		}
	}
	for _, f := range d.FactsAdded {
		add(f.Name)
	}
	for _, f := range d.FactsRemoved {
		add(f.Name)
	}
	for _, c := range d.FactsChanged {
		add(c.After.Name)
	}
	for _, e := range d.EdgesAdded {
		add(e.Source)
		add(e.Target)
	}
	for _, e := range d.EdgesRemoved {
		add(e.Source)
		add(e.Target)
	}
	return m
}

// findingHasStructuralCause reports whether any entity the finding cites was
// structurally touched by this change. Evidence-less findings can't be attributed,
// so they default to real (never silently hidden).
func findingHasStructuralCause(in facts.Insight, touched map[string]struct{}) bool {
	if len(in.Evidence) == 0 {
		return true
	}
	for _, ev := range in.Evidence {
		for _, e := range []string{ev.Fact, ev.Symbol, ev.File} {
			if e == "" {
				continue
			}
			if _, ok := touched[e]; ok {
				return true
			}
		}
	}
	return false
}

// Empty reports whether the diff contains no changes of any kind.
func (d *SnapshotDiff) Empty() bool {
	return len(d.FactsAdded) == 0 && len(d.FactsRemoved) == 0 && len(d.FactsChanged) == 0 &&
		len(d.EdgesAdded) == 0 && len(d.EdgesRemoved) == 0 &&
		len(d.FindingsNew) == 0 && len(d.FindingsResolved) == 0 &&
		len(d.FindingsNewIncidental) == 0 && len(d.FindingsResolvedIncidental) == 0 &&
		len(d.FindingsChanged) == 0
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
		Comparability:       d.Comparability,
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
	for _, in := range d.FindingsNewIncidental {
		if insightMatches(in, focus) {
			out.FindingsNewIncidental = append(out.FindingsNewIncidental, in)
		}
	}
	for _, in := range d.FindingsResolvedIncidental {
		if insightMatches(in, focus) {
			out.FindingsResolvedIncidental = append(out.FindingsResolvedIncidental, in)
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

// groupByKey buckets facts by factKey, preserving every member rather than
// letting the last one win.
//
// factKey is NOT unique even with a discriminator, and no prop can make it so:
// Swift declares the same class twice under mutually exclusive #if/#else
// branches, overloaded methods share a name and symbol_kind, and a dependency
// fact's name already embeds its import target, so two imports of one target in
// one file are identical in name, props AND relations. Such facts differ only
// by line. Keying them into a map[string]Fact silently dropped all but one, so
// a deletion produced no diff entry and — because the on-disk baseline and the
// in-memory current iterate facts in different orders — the surviving
// representative differed between the two sides, reporting a "changed" fact
// between byte-identical fact sets.
//
// Each bucket is sorted by intraGroupOrder so the two sides pair positionally.
func groupByKey(fs []facts.Fact) map[string][]facts.Fact {
	groups := make(map[string][]facts.Fact, len(fs))
	for _, f := range fs {
		k := factKey(f)
		groups[k] = append(groups[k], f)
	}
	for _, g := range groups {
		if len(g) > 1 {
			sort.Slice(g, func(i, j int) bool { return intraGroupOrder(g[i]) < intraGroupOrder(g[j]) })
		}
	}
	return groups
}

// intraGroupOrder totally orders facts that share a factKey. Line comes first
// because it is the only field that reliably separates them and it is stable
// under the edits that matter: an insertion above the group shifts every member
// equally, preserving their relative order, so the pairing does not churn.
//
// This is the ONLY place line participates in matching, and only WITHIN a group
// that already shares an identity. It never enters factKey, so a lone fact that
// merely moves is still unchanged — see TestCompute_LineShiftIsNotAChange.
func intraGroupOrder(f facts.Fact) string {
	return fmt.Sprintf("%09d\x00%s\x00%s", f.Line, propsJSON(f.Props), relationsJSON(f.Relations))
}

// relationsJSON renders relations order-stably for use as a tiebreak.
func relationsJSON(rs []facts.Relation) string {
	if len(rs) == 0 {
		return ""
	}
	b, err := json.Marshal(rs)
	if err != nil {
		return ""
	}
	return string(b)
}

// factDiscriminator returns the props that distinguish facts which legitimately
// share (kind, repo, file, name). It is kind-specific because only a few kinds
// have multiple facts per name; for the rest the fully-qualified name is unique.
// It deliberately uses identity-bearing props (a route's method, a storage
// reference's operation), not mutable ones, so a genuine attribute change still
// surfaces as "changed" rather than remove+add.
//
// It is no longer load-bearing for correctness — groupByKey handles collisions
// it cannot separate — but it still improves pairing: a route's GET and DELETE
// facts match up by method rather than by position.
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

// factSortKey totally orders facts for deterministic output. It extends factKey
// with intraGroupOrder so that facts sharing an identity still have a stable
// relative order in the rendered diff.
func factSortKey(f facts.Fact) string {
	return factKey(f) + "\x00" + intraGroupOrder(f)
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

// titleNumber matches the volatile metrics embedded in finding titles (counts,
// ratios, percentages) so they can be stripped for a stable identity.
var titleNumber = regexp.MustCompile(`[0-9]+(\.[0-9]+)?`)

// normalizeTitle removes the volatile numbers from a finding title, leaving the
// stable subject. "Large public surface: x/y exports 67 of 67 symbols (100%)"
// and the same line with different counts collapse to one identity.
func normalizeTitle(s string) string {
	return titleNumber.ReplaceAllString(s, "#")
}

// groupInsightsByKey buckets findings that share a findingKey, so a group of
// identically-identified findings is compared member-for-member instead of collapsing
// to one. Each bucket is sorted by intraInsightOrder so the two sides pair positionally
// — the same contract groupByKey has for facts.
func groupInsightsByKey(ins []facts.Insight) map[string][]facts.Insight {
	groups := make(map[string][]facts.Insight, len(ins))
	for _, in := range ins {
		k := findingKey(in)
		groups[k] = append(groups[k], in)
	}
	for _, g := range groups {
		if len(g) > 1 {
			sort.Slice(g, func(i, j int) bool { return intraInsightOrder(g[i]) < intraInsightOrder(g[j]) })
		}
	}
	return groups
}

// intraInsightOrder totally orders findings that share a findingKey. Evidence comes
// first because it is what actually separates them (78 layer violations share a title
// and differ only in the module they cite); the title breaks the remaining ties.
//
// Like intraGroupOrder for facts, this participates ONLY in pairing within a group that
// already shares an identity. It never enters findingKey, so evidence churn on a lone
// finding is still not an identity change — see TestCompute_SummaryFindingDoesNotChurn.
func intraInsightOrder(in facts.Insight) string {
	return sortedEvidenceEntities(in) + "\x00" + in.Title
}

// insightChanged reports whether two findings under the same identity differ in
// content. Deliberately excludes nothing: the title (whose embedded count is the
// payload for the dead-code rollup), the confidence, the description and the evidence
// all carry meaning a reader would want to know moved.
func insightChanged(a, b facts.Insight) bool {
	if a.Title != b.Title || a.Confidence != b.Confidence || a.Description != b.Description {
		return true
	}
	return evidenceJSON(a.Evidence) != evidenceJSON(b.Evidence)
}

// evidenceJSON renders a finding's evidence order-stably for content comparison.
func evidenceJSON(ev []facts.Evidence) string {
	if len(ev) == 0 {
		return ""
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return ""
	}
	return string(b)
}

// findingKey identifies an insight so a finding stays the SAME finding across
// snapshots even as its metrics drift or a ranked list re-orders.
//
// Most explainers name their subject (module/symbol/repo/pattern) in the title
// and vary only by counts, so the number-normalized title is the stable identity.
// This is what stops whole-codebase "summary" findings (e.g. the layers pattern,
// whose evidence enumerates every module) from churning resolve+introduce on any
// edit. Cycles are the exception: their title carries only a member count, so two
// distinct cycles would collide — they are keyed on their sorted member modules
// (the evidence), which is also what makes a cycle stay identified as long as its
// membership holds.
func findingKey(in facts.Insight) string {
	if in.Source == "cycles" {
		return in.Source + "\x00" + sortedEvidenceEntities(in)
	}
	return in.Source + "\x00" + normalizeTitle(in.Title)
}

// sortedEvidenceEntities joins a finding's cited entities (Fact/Symbol/File) in
// sorted order — a stable identity for set-defined findings like cycles.
func sortedEvidenceEntities(in facts.Insight) string {
	var ents []string
	for _, ev := range in.Evidence {
		if e := firstNonEmpty(ev.Fact, ev.Symbol, ev.File); e != "" {
			ents = append(ents, e)
		}
	}
	sort.Strings(ents)
	return strings.Join(ents, "\x1f")
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
//
// Facts are ordered by factSortKey, not factKey: colliding facts share a factKey,
// and the collections are built by ranging over a map, so factKey alone would
// leave their relative order to sort.Slice and to Go's randomized map iteration.
func (d *SnapshotDiff) sortAll() {
	sort.Slice(d.FactsAdded, func(i, j int) bool { return factSortKey(d.FactsAdded[i]) < factSortKey(d.FactsAdded[j]) })
	sort.Slice(d.FactsRemoved, func(i, j int) bool {
		return factSortKey(d.FactsRemoved[i]) < factSortKey(d.FactsRemoved[j])
	})
	sort.Slice(d.FactsChanged, func(i, j int) bool {
		return factSortKey(d.FactsChanged[i].After) < factSortKey(d.FactsChanged[j].After)
	})
	sort.Slice(d.EdgesAdded, func(i, j int) bool { return edgeKey(d.EdgesAdded[i]) < edgeKey(d.EdgesAdded[j]) })
	sort.Slice(d.EdgesRemoved, func(i, j int) bool { return edgeKey(d.EdgesRemoved[i]) < edgeKey(d.EdgesRemoved[j]) })
	// findingKey alone is NOT a total order — findings collide under it (78 of
	// a large Android app's layer violations share one key). Ties would sort
	// arbitrarily and the diff would stop being byte-reproducible, which is the
	// package's central promise. Break them with intraInsightOrder, exactly as the fact
	// buckets break theirs with intraGroupOrder.
	byFinding := func(s []facts.Insight) func(i, j int) bool {
		return func(i, j int) bool { return findingSortKey(s[i]) < findingSortKey(s[j]) }
	}
	sort.Slice(d.FindingsNew, byFinding(d.FindingsNew))
	sort.Slice(d.FindingsResolved, byFinding(d.FindingsResolved))
	sort.Slice(d.FindingsNewIncidental, byFinding(d.FindingsNewIncidental))
	sort.Slice(d.FindingsResolvedIncidental, byFinding(d.FindingsResolvedIncidental))
	sort.Slice(d.FindingsChanged, func(i, j int) bool {
		return findingSortKey(d.FindingsChanged[i].After) < findingSortKey(d.FindingsChanged[j].After)
	})
}

// findingSortKey totally orders findings for deterministic rendering: identity first,
// then whatever separates findings that share it.
func findingSortKey(in facts.Insight) string {
	return findingKey(in) + "\x00" + intraInsightOrder(in)
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
