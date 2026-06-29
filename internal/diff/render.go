package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// RenderSummary is the default, smallest view: the headline regressions and
// improvements plus a structural-change tally. It deliberately omits any listing
// of pre-existing state — only deltas appear.
func (d *SnapshotDiff) RenderSummary() string {
	var sb strings.Builder
	sb.WriteString("# Architecture diff\n\n")
	d.writeProvenance(&sb)

	if d.Empty() {
		sb.WriteString("**No architectural changes detected.** The change did not add, remove, or alter any facts, edges, or findings.\n")
		return sb.String()
	}

	// Regressions first — this is the signal an agent most needs.
	sb.WriteString(fmt.Sprintf("## Regressions introduced (%d)\n\n", len(d.FindingsNew)))
	if len(d.FindingsNew) == 0 {
		sb.WriteString("None — the change introduced no new findings.\n\n")
	} else {
		for _, in := range d.FindingsNew {
			fmt.Fprintf(&sb, "- [%s] %.2f — %s\n", insightSource(in), in.Confidence, oneLine(in.Title))
		}
		sb.WriteString("\n_Confidence < 1.0 is a candidate to verify, not a verdict. Use output_mode='compact' for descriptions, evidence, and caveats._\n\n")
	}

	if len(d.FindingsResolved) > 0 {
		sb.WriteString(fmt.Sprintf("## Improvements — findings resolved (%d)\n\n", len(d.FindingsResolved)))
		for _, in := range d.FindingsResolved {
			fmt.Fprintf(&sb, "- [%s] %s\n", insightSource(in), oneLine(in.Title))
		}
		sb.WriteString("\n")
	}

	d.writeStructuralSummary(&sb)
	return sb.String()
}

// RenderCompact adds, on top of the summary, per-finding descriptions with an
// evidence sample, and capped lists of added/removed edges and facts so the
// caller can see exactly what changed without fetching the full JSON.
func (d *SnapshotDiff) RenderCompact() string {
	const sample = 25

	var sb strings.Builder
	sb.WriteString("# Architecture diff\n\n")
	d.writeProvenance(&sb)

	if d.Empty() {
		sb.WriteString("**No architectural changes detected.**\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("## Regressions introduced (%d)\n\n", len(d.FindingsNew)))
	if len(d.FindingsNew) == 0 {
		sb.WriteString("None — the change introduced no new findings.\n\n")
	} else {
		for i, in := range d.FindingsNew {
			fmt.Fprintf(&sb, "### %d. %s\n", i+1, in.Title)
			fmt.Fprintf(&sb, "- explainer: %s · confidence: %.2f\n", insightSource(in), in.Confidence)
			if in.Description != "" {
				fmt.Fprintf(&sb, "- %s\n", in.Description)
			}
			writeEvidenceSample(&sb, in.Evidence, 8)
			if len(in.Actions) > 0 {
				sb.WriteString("- suggested actions:\n")
				for _, a := range in.Actions {
					fmt.Fprintf(&sb, "    - %s\n", a)
				}
			}
			sb.WriteString("\n")
		}
	}

	if len(d.FindingsResolved) > 0 {
		sb.WriteString(fmt.Sprintf("## Improvements — findings resolved (%d)\n\n", len(d.FindingsResolved)))
		for _, in := range d.FindingsResolved {
			fmt.Fprintf(&sb, "- [%s] %s\n", insightSource(in), oneLine(in.Title))
		}
		sb.WriteString("\n")
	}

	d.writeStructuralSummary(&sb)

	// New coupling is the architecturally interesting structural change, so list
	// edges before the bulk fact lists.
	writeEdgeList(&sb, "New coupling — edges added", d.EdgesAdded, sample)
	writeEdgeList(&sb, "Coupling removed — edges removed", d.EdgesRemoved, sample)
	writeFactList(&sb, "Facts added", d.FactsAdded, sample)
	writeFactList(&sb, "Facts removed", d.FactsRemoved, sample)

	if len(d.FactsChanged) > 0 {
		fmt.Fprintf(&sb, "## Facts changed (%d)\n\n", len(d.FactsChanged))
		shown := d.FactsChanged
		if len(shown) > sample {
			shown = shown[:sample]
		}
		for _, c := range shown {
			fmt.Fprintf(&sb, "- %s %s (%s:%d)\n", c.After.Kind, c.After.Name, c.After.File, c.After.Line)
		}
		if len(d.FactsChanged) > len(shown) {
			fmt.Fprintf(&sb, "- … and %d more (output_mode='full' for all)\n", len(d.FactsChanged)-len(shown))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func (d *SnapshotDiff) writeProvenance(sb *strings.Builder) {
	if d.BaselineGeneratedAt != "" || d.CurrentGeneratedAt != "" {
		fmt.Fprintf(sb, "_Baseline %s → current %s._\n\n", orDash(d.BaselineGeneratedAt), orDash(d.CurrentGeneratedAt))
	}
	// A baseline taken on a different repo makes the delta meaningless; flag it.
	if d.BaselineRepo != "" && d.CurrentRepo != "" && d.BaselineRepo != d.CurrentRepo {
		fmt.Fprintf(sb, "> ⚠️ Baseline repo (%s) differs from current (%s); this diff is not meaningful.\n\n", d.BaselineRepo, d.CurrentRepo)
	}
}

// writeStructuralSummary renders the added/removed tally per fact kind plus edges.
func (d *SnapshotDiff) writeStructuralSummary(sb *strings.Builder) {
	added := KindCounts(d.FactsAdded)
	removed := KindCounts(d.FactsRemoved)

	sb.WriteString("## Structural changes\n\n")

	kinds := map[string]struct{}{}
	for k := range added {
		kinds[k] = struct{}{}
	}
	for k := range removed {
		kinds[k] = struct{}{}
	}
	if len(kinds) > 0 {
		ordered := make([]string, 0, len(kinds))
		for k := range kinds {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		for _, k := range ordered {
			fmt.Fprintf(sb, "- %s: +%d / -%d\n", k, added[k], removed[k])
		}
	}
	fmt.Fprintf(sb, "- edges: +%d / -%d\n", len(d.EdgesAdded), len(d.EdgesRemoved))
	if len(d.FactsChanged) > 0 {
		fmt.Fprintf(sb, "- facts changed (props): %d\n", len(d.FactsChanged))
	}
	sb.WriteString("\n")
}

func writeEdgeList(sb *strings.Builder, heading string, edges []Edge, limit int) {
	if len(edges) == 0 {
		return
	}
	fmt.Fprintf(sb, "## %s (%d)\n\n", heading, len(edges))
	shown := edges
	if len(shown) > limit {
		shown = shown[:limit]
	}
	for _, e := range shown {
		fmt.Fprintf(sb, "- %s --%s--> %s\n", e.Source, e.Kind, e.Target)
	}
	if len(edges) > len(shown) {
		fmt.Fprintf(sb, "- … and %d more (output_mode='full' for all)\n", len(edges)-len(shown))
	}
	sb.WriteString("\n")
}

func writeFactList(sb *strings.Builder, heading string, ff []facts.Fact, limit int) {
	if len(ff) == 0 {
		return
	}
	fmt.Fprintf(sb, "## %s (%d)\n\n", heading, len(ff))
	shown := ff
	if len(shown) > limit {
		shown = shown[:limit]
	}
	for _, f := range shown {
		fmt.Fprintf(sb, "- %s %s (%s:%d)\n", f.Kind, f.Name, f.File, f.Line)
	}
	if len(ff) > len(shown) {
		fmt.Fprintf(sb, "- … and %d more (output_mode='full' for all)\n", len(ff)-len(shown))
	}
	sb.WriteString("\n")
}

func writeEvidenceSample(sb *strings.Builder, evidence []facts.Evidence, limit int) {
	if len(evidence) == 0 {
		return
	}
	fmt.Fprintf(sb, "- evidence (%d):\n", len(evidence))
	shown := len(evidence)
	if shown > limit {
		shown = limit
	}
	for _, ev := range evidence[:shown] {
		fmt.Fprintf(sb, "    - %s\n", formatEvidence(ev))
	}
	if len(evidence) > shown {
		fmt.Fprintf(sb, "    - … and %d more (output_mode='full' for all)\n", len(evidence)-shown)
	}
}

// --- small formatting helpers (kept local to avoid coupling to the server package) ---

func insightSource(in facts.Insight) string {
	if in.Source == "" {
		return "—"
	}
	return in.Source
}

func formatEvidence(ev facts.Evidence) string {
	var parts []string
	for _, p := range []string{ev.Fact, ev.Symbol, ev.File} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	s := strings.Join(parts, " ")
	if ev.Detail != "" {
		if s != "" {
			s += " — " + ev.Detail
		} else {
			s = ev.Detail
		}
	}
	return s
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", "\\|")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
