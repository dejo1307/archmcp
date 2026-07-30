package check

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

// kindMeaning explains, per warning kind, what it does to the delta — so the output
// says why a baseline is untrustworthy or merely caveated rather than only that it is.
var kindMeaning = map[diff.WarningKind]string{
	diff.WarnDifferentRepo:   "the two snapshots are of different repositories, so the delta is not about your change",
	diff.WarnVersionMismatch: "different enola versions extract differently, so unchanged code can appear as churn",
	diff.WarnExtractorSet:    "a language present on one side only makes all of its facts appear added or removed",
	diff.WarnIgnoreGlobs:     "the set of files parsed changed, so some of this delta is exclusion changes, not code changes",
	diff.WarnUnclassified:    "an uncategorized caveat was raised; the gate fails closed rather than grade what it cannot judge",
	diff.WarnStaleBaseline:   "the delta is real, but it also contains whatever the repository itself changed since the baseline was pinned",
	diff.WarnPreReceipt:      "the baseline has no recorded version or extractor set, so comparability could not be fully verified",
	diff.WarnInvertedPair:    "the current snapshot predates the baseline, so it does not contain your change",
}

// Render is the human-readable verdict: the headline, why the gate did or did not
// grade, then the delta itself from internal/diff.
func (v Verdict) Render() string {
	var sb strings.Builder

	switch v.Status {
	case StatusClean:
		switch {
		case len(v.Failures) > 0:
			// --warn-only. Say what the policy WOULD have done: reporting "no structural
			// regression" here would be false, and it is the line a reader skims.
			fmt.Fprintf(&sb, "PASS (--warn-only) — %s reported, not failed.\n",
				plural(len(v.Failures), "structural regression", "structural regressions"))
		case len(v.Advisories) > 0 || v.EdgesAdded > 0 || v.FactsAdded > 0 || v.FactsRemoved > 0:
			sb.WriteString("PASS — no structural regression.\n")
		default:
			sb.WriteString("PASS — no architectural change.\n")
		}
	case StatusRegression:
		fmt.Fprintf(&sb, "FAIL — %s introduced.\n", plural(len(v.Failures), "structural regression", "structural regressions"))
	case StatusUsageError:
		sb.WriteString("ERROR — the gate could not run.\n")
	case StatusIncomparable:
		sb.WriteString("DECLINED — refusing to grade: the baseline is not comparable to the current snapshot.\n")
		sb.WriteString("This is NOT a statement about your change. The delta below would describe how the two\nsnapshots were produced, not what you edited.\n")
	}

	v.writeComparability(&sb)

	if len(v.Failures) > 0 {
		// The header has to agree with the verdict. Labelling these "(fail)" under a
		// DECLINED or ERROR status contradicts the headline that just said the gate did
		// not grade — and the whole point of a distinct exit code is that a caller is
		// never told a change is bad when the comparison was untrustworthy.
		var header string
		switch {
		case v.Status == StatusIncomparable || v.Status == StatusUsageError:
			header = "Findings in this delta (NOT graded — see above)"
		case v.Policy.WarnOnly:
			header = "Regressions (would fail; --warn-only)"
		default:
			header = "Regressions (fail)"
		}
		fmt.Fprintf(&sb, "\n%s:\n", header)
		writeFindings(&sb, v.Failures)
		if v.Status != StatusIncomparable && v.Status != StatusUsageError {
			fmt.Fprintf(&sb, "\nPolicy: fail on new findings from [%s] at confidence >= %.2f.\n",
				strings.Join(v.Policy.failExplainers(), ", "), v.Policy.minConfidence())
		}
	}

	if len(v.Advisories) > 0 {
		sb.WriteString("\nNew findings (advisory — below the failure policy):\n")
		writeFindings(&sb, v.Advisories)
		sb.WriteString("\nConfidence < 1.00 is a candidate to verify, not a verdict.\n")
	}

	if len(v.Resolved) > 0 {
		fmt.Fprintf(&sb, "\nResolved by this change (%d):\n", len(v.Resolved))
		writeFindings(&sb, v.Resolved)
	}

	// Findings first (graded, then resolved, then merely moved), structure after: the
	// reader is asking "is anything wrong?" before "what did I touch?".
	v.writeAlteredFindings(&sb)

	if len(v.Incidental) > 0 {
		fmt.Fprintf(&sb, "\nIncidental shifts (%d) — findings that moved with no structural cause in this\nchange (a drifting statistical threshold or a re-ranked list). Never graded.\n",
			len(v.Incidental))
	}

	v.writeWhatChanged(&sb)

	return sb.String()
}

// listCap is how many entries each list prints before summarizing the rest. Enough to
// recognize what a change touched; short enough that a clean run stays scannable.
const listCap = 12

// writeWhatChanged renders the structural delta as the thing it describes rather than as
// two numbers. "+4/0 facts, +15/0 edges" is true and nearly useless — it tells you
// something moved without telling you what, so the only way to find out was to re-run with
// --detail or go read the files, which is the work the gate exists to replace.
func (v Verdict) writeWhatChanged(sb *strings.Builder) {
	if v.FactsAdded == 0 && v.FactsRemoved == 0 && v.FactsChanged == 0 && v.EdgesAdded == 0 && v.EdgesRemoved == 0 {
		return
	}

	sb.WriteString("\nWhat changed\n")
	for _, kind := range sortedKinds(v.AddedByKind, v.RemovedByKind) {
		fmt.Fprintf(sb, "  %-12s %s\n", pluralKind(kind), signed(v.AddedByKind[kind], v.RemovedByKind[kind]))
	}
	if v.FactsChanged > 0 {
		fmt.Fprintf(sb, "  %-12s %d altered (a signature, an export, a metric)\n", "attributes", v.FactsChanged)
	}
	if v.EdgesAdded > 0 || v.EdgesRemoved > 0 {
		fmt.Fprintf(sb, "  %-12s %s%s\n", "edges", signed(v.EdgesAdded, v.EdgesRemoved),
			edgeKindBreakdown(v.EdgeKindsAdded, v.EdgeKindsRemoved))
	}

	if v.Diff == nil {
		return
	}
	writeFactLines(sb, "Added", v.Diff.FactsAdded)
	writeFactLines(sb, "Removed", v.Diff.FactsRemoved)
	writeEdgeLines(sb, "New coupling", v.Diff.EdgesAdded)
	writeEdgeLines(sb, "Removed coupling", v.Diff.EdgesRemoved)

	if v.EdgesAdded > 0 {
		sb.WriteString("\nNew coupling is reported, not failed: an added call edge is what ordinary work\nlooks like. Inspect the list above if it is more than you expected.\n")
	}
}

// writeAlteredFindings surfaces findings that survived but moved. Without it a rollup
// swinging by thousands of symbols lands nowhere, and the gate prints a confident PASS.
func (v Verdict) writeAlteredFindings(sb *strings.Builder) {
	if len(v.FindingsChanged) == 0 {
		return
	}
	// Split the ones with something to SHOW from the ones that merely re-ranked their
	// evidence. Listing both together printed a dozen near-identical lines whose only
	// visible delta was "confidence 0.77 -> 0.77" — a difference real in float but not at
	// any precision worth reading, which trains the reader to skip the section.
	var shown []diff.InsightChange
	evidenceOnly := 0
	for _, ch := range v.FindingsChanged {
		if displayDiffers(ch) {
			shown = append(shown, ch)
		} else {
			evidenceOnly++
		}
	}

	if len(shown) > 0 {
		fmt.Fprintf(sb, "\nAltered findings (%d) — same finding, moved content:\n", len(shown))
		for i, ch := range shown {
			if i == listCap {
				fmt.Fprintf(sb, "  … %d more (--detail for the full list)\n", len(shown)-listCap)
				break
			}
			source := ch.After.Source
			if source == "" {
				source = "unknown"
			}
			fmt.Fprintf(sb, "  - [%s] %s\n", source, oneLine(ch.After.Title))
			if before, after := oneLine(ch.Before.Title), oneLine(ch.After.Title); before != after {
				fmt.Fprintf(sb, "      was: %s\n", before)
			}
			if b, a := conf(ch.Before.Confidence), conf(ch.After.Confidence); b != a {
				fmt.Fprintf(sb, "      confidence %s -> %s\n", b, a)
			}
		}
	}
	if evidenceOnly > 0 {
		fmt.Fprintf(sb, "\n%s changed only in supporting evidence (same title and confidence) —\ntypically a fan-in or top-N list gaining a member. Use --detail to see them.\n",
			plural(evidenceOnly, "finding", "findings"))
	}
}

// displayDiffers reports whether a finding's change is visible at the precision this
// report prints. A confidence that moved in the third decimal is a change the diff is
// right to record and this report has nothing useful to say about.
func displayDiffers(ch diff.InsightChange) bool {
	return oneLine(ch.Before.Title) != oneLine(ch.After.Title) ||
		conf(ch.Before.Confidence) != conf(ch.After.Confidence)
}

func conf(f float64) string { return fmt.Sprintf("%.2f", f) }

// writeFactLines lists facts grouped by kind, so added modules do not hide among symbols.
func writeFactLines(sb *strings.Builder, heading string, ff []facts.Fact) {
	if len(ff) == 0 {
		return
	}
	fmt.Fprintf(sb, "\n%s (%d):\n", heading, len(ff))
	shown := 0
	for _, kind := range sortedKinds(diff.KindCounts(ff), nil) {
		for _, f := range ff {
			if f.Kind != kind {
				continue
			}
			if shown == listCap {
				fmt.Fprintf(sb, "  … %d more (--detail for the full list)\n", len(ff)-shown)
				return
			}
			fmt.Fprintf(sb, "  %-10s %-44s %s\n", f.Kind, truncate(f.Name, 44), location(f))
			shown++
		}
	}
}

// edgeRank orders relations by how much they say about coupling. `declares` is last: it is
// the mechanical symbol→module link every new symbol brings with it, so it dominates the
// count on any change that adds code while saying nothing about what got coupled to what.
var edgeRank = map[string]int{
	"imports": 0, "depends_on": 1, "calls": 2, "implements": 3,
	"instantiates": 4, "injects": 5, "has_method": 6,
	"declares": rankDeclares,
}

const (
	// rankUnknown is where a relation this list has not been taught about sorts: after
	// the known meaningful ones, but still ahead of `declares`. A relation kind we do not
	// recognize is more likely to be worth reading than the mechanical one we do.
	rankUnknown = 50
	// rankDeclares is last — see the comment on edgeRank.
	rankDeclares = 99
)

func rankOf(kind string) int {
	if r, ok := edgeRank[kind]; ok {
		return r
	}
	return rankUnknown
}

// edgeKindBreakdown renders " (calls +38, declares +16)" so the headline edge count is
// readable rather than merely large.
func edgeKindBreakdown(added, removed map[string]int) string {
	kinds := map[string]bool{}
	for k := range added {
		kinds[k] = true
	}
	for k := range removed {
		kinds[k] = true
	}
	if len(kinds) == 0 {
		return ""
	}
	ordered := make([]string, 0, len(kinds))
	for k := range kinds {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool {
		ri, rj := rankOf(ordered[i]), rankOf(ordered[j])
		if ri != rj {
			return ri < rj
		}
		return ordered[i] < ordered[j]
	})
	parts := make([]string, 0, len(ordered))
	for _, k := range ordered {
		parts = append(parts, k+" "+signed(added[k], removed[k]))
	}
	return "  (" + strings.Join(parts, ", ") + ")"
}

func writeEdgeLines(sb *strings.Builder, heading string, edges []diff.Edge) {
	if len(edges) == 0 {
		return
	}
	// Copy before sorting: the caller owns this slice (it is the diff's own field), and
	// reordering it in place would mutate the delta that --detail and --json then render.
	ordered := append([]diff.Edge(nil), edges...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ri, rj := rankOf(ordered[i].Kind), rankOf(ordered[j].Kind)
		if ri != rj {
			return ri < rj
		}
		if ordered[i].Source != ordered[j].Source {
			return ordered[i].Source < ordered[j].Source
		}
		return ordered[i].Target < ordered[j].Target
	})

	fmt.Fprintf(sb, "\n%s (%d):\n", heading, len(ordered))
	for i, e := range ordered {
		if i == listCap {
			fmt.Fprintf(sb, "  … %d more (--detail for the full list)\n", len(ordered)-listCap)
			return
		}
		fmt.Fprintf(sb, "  %-44s --%s--> %s\n", truncate(edgeSource(e), 44), e.Kind, e.Target)
	}
}

// edgeSource strips the embedded target from a dependency fact's name. Those are named
// "<importer> -> <imported>", so rendering one verbatim produced
// "pkg/check -> sort --imports--> sort" — the target stated twice, once in an arrow
// notation that collides with the one this line already uses.
func edgeSource(e diff.Edge) string {
	if i := strings.Index(e.Source, " -> "); i > 0 {
		return e.Source[:i]
	}
	return e.Source
}

// location renders where a fact lives, with the line only when there is one — a module has
// no meaningful line, and printing ":0" for it looks like a bug.
func location(f facts.Fact) string {
	switch {
	case f.File == "":
		return ""
	case f.Line > 0:
		return fmt.Sprintf("%s:%d", f.File, f.Line)
	default:
		return f.File
	}
}

// sortedKinds returns the union of the two maps' keys in a stable, meaningful order:
// architectural kinds in their conceptual order first, anything unrecognized after,
// alphabetically. Map iteration order would otherwise make the output non-deterministic,
// which for a tool that sells reproducibility would be a poor look.
func sortedKinds(a, b map[string]int) []string {
	order := map[string]int{
		facts.KindService: 0, facts.KindModule: 1, facts.KindSymbol: 2,
		facts.KindRoute: 3, facts.KindStorage: 4, facts.KindDependency: 5,
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]int{a, b} {
		for k, n := range m {
			if n > 0 && !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oki := order[out[i]]
		oj, okj := order[out[j]]
		if oki != okj {
			return oki
		}
		if oki && okj && oi != oj {
			return oi < oj
		}
		return out[i] < out[j]
	})
	return out
}

// signed renders an added/removed pair, omitting a zero side so "+3" is not written "+3/-0".
func signed(added, removed int) string {
	switch {
	case added > 0 && removed > 0:
		return fmt.Sprintf("+%d / -%d", added, removed)
	case removed > 0:
		return fmt.Sprintf("-%d", removed)
	default:
		return fmt.Sprintf("+%d", added)
	}
}

// kindPlural spells the architectural kinds out, because appending "s" produces
// "dependencys" and "storages".
var kindPlural = map[string]string{
	facts.KindService:    "services",
	facts.KindModule:     "modules",
	facts.KindSymbol:     "symbols",
	facts.KindRoute:      "routes",
	facts.KindStorage:    "storage",
	facts.KindDependency: "dependencies",
}

func pluralKind(kind string) string {
	if p, ok := kindPlural[kind]; ok {
		return p
	}
	if strings.HasSuffix(kind, "s") {
		return kind
	}
	return kind + "s"
}

// truncate shortens an identifier from the LEFT, keeping its tail.
//
// Fact and edge names here are path-like — "airflow-core/src/airflow/pkg/__init__.helper"
// — and everything that distinguishes one from another lives at the end: the file, the
// symbol. Cutting the tail produced output where genuinely different entries rendered
// identically, so a list of seven distinct new edges read as though it contained
// duplicates. The shared prefix is the part a reader can afford to lose.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	// Byte slicing is safe against a multi-byte boundary here because the cut point is
	// re-derived from a rune count, not from the byte index.
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return "…" + string(runes[len(runes)-(max-1):])
}

// writeComparability prints every warning verbatim plus what its category means for
// the delta, so "stale" or "incomparable" is never asserted without its reason.
func (v Verdict) writeComparability(sb *strings.Builder) {
	if len(v.ComparabilityWarnings) == 0 {
		return
	}
	sb.WriteString("\nBaseline comparability:\n")
	for _, w := range v.ComparabilityWarnings {
		fmt.Fprintf(sb, "  - %s\n", w)
	}
	if len(v.BlockingKinds) > 0 {
		sb.WriteString("\n  Blocking (the gate declined to grade):\n")
		writeKinds(sb, v.BlockingKinds)
	}
	if len(v.AdvisoryKinds) > 0 {
		sb.WriteString("\n  Advisory (graded anyway):\n")
		writeKinds(sb, v.AdvisoryKinds)
	}
}

func writeKinds(sb *strings.Builder, kinds []diff.WarningKind) {
	for _, k := range kinds {
		if m := kindMeaning[k]; m != "" {
			fmt.Fprintf(sb, "    %s — %s\n", k, m)
		} else {
			fmt.Fprintf(sb, "    %s\n", k)
		}
	}
}

func writeFindings(sb *strings.Builder, ins []facts.Insight) {
	for _, in := range ins {
		source := in.Source
		if source == "" {
			source = "unknown"
		}
		fmt.Fprintf(sb, "  - [%s] %.2f — %s\n", source, in.Confidence, oneLine(in.Title))
		for _, ev := range in.Evidence {
			if d := strings.TrimSpace(ev.Detail); d != "" {
				fmt.Fprintf(sb, "      %s\n", oneLine(d))
				break
			}
		}
	}
}

// Detail returns the full delta report from internal/diff, for callers that want the
// changed edges and facts under the verdict rather than only the graded findings.
func (v Verdict) Detail() string {
	if v.Diff == nil {
		return ""
	}
	return v.Diff.RenderCompact()
}

// JSON is the machine-readable verdict, for a CI step that wants to post the result
// rather than parse the text.
func (v Verdict) JSON() ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// oneLine flattens prose to a single line so a multi-line description cannot break
// the one-finding-per-line shape the output relies on.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
