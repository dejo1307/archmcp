package dashboard

import (
	"math"
	"sort"

	"github.com/enola-labs/enola/pkg/facts"
)

// insightRow is one insight in a category group of the Insights modal.
type insightRow struct {
	Title      string
	Confidence int  // 0-100
	Structural bool // confidence >= 1.0 — a certain (non-heuristic) finding
	Evidence   string
}

// insightGroup is all insights produced by one explainer (Source), with a bar
// width proportional to its share of the largest group.
type insightGroup struct {
	Source string
	Label  string
	Count  int
	BarPct int // 0-100, relative to the largest group
	Items  []insightRow
}

// insightLabels maps an explainer Source id to a human-friendly label, one entry
// per explainer bootstrap.NewEngine registers. The engine has no display-name
// registry, so this mirror lives here.
//
// It doubles as the ADMISSION LIST: insightDetails renders only sources found
// here. Both binaries share a repo's .enola/insights.json, so a file written by
// a build with extra explainers must not leak findings this engine cannot
// produce into this engine's dashboard. A wrapper widens the list — labelling
// and admitting in one step — through Options.InsightLabels.
var insightLabels = map[string]string{
	"hotspots":            "Hotspots",
	"god-class":           "God classes",
	"exported-surface":    "Exported surface",
	"complexity-outliers": "Complexity outliers",
	"dependency-depth":    "Dependency depth",
	"cycles":              "Dependency cycles",
	"layers":              "Layer violations",
	"crossrepo":           "Cross-repo dependencies",
	"coverage":            "Coverage gaps",
	"unused-routes":       "Unused routes",
	"domain":              "Domain boundaries",
	"query-loops":         "Query loops",
	"entry-points":        "Entry points",
	"messaging-coverage":  "Messaging coverage",
	"intent":              "Intent",
	"constraints":         "Constraint violations",
}

// mergedLabels returns the engine's label map widened by a wrapper's extra
// entries. The result is a copy, so a Server never mutates package state and two
// dashboards in one process cannot see each other's labels.
func mergedLabels(extra map[string]string) map[string]string {
	merged := make(map[string]string, len(insightLabels)+len(extra))
	for source, label := range insightLabels {
		merged[source] = label
	}
	for source, label := range extra {
		merged[source] = label
	}
	return merged
}

// firstEvidence returns the most locating evidence string for an insight: the first
// evidence's File, else Symbol, else Fact. Empty when there is no evidence.
func firstEvidence(ev []facts.Evidence) string {
	for _, e := range ev {
		switch {
		case e.File != "":
			return e.File
		case e.Symbol != "":
			return e.Symbol
		case e.Fact != "":
			return e.Fact
		}
	}
	return ""
}

// insightDetails groups insights by explainer Source for the modal: one bar per
// group (count-ranked, width relative to the largest group), each expandable to its
// insights (sorted certain-first). It also returns the structural vs candidate split
// (confidence == 1.0 vs < 1.0) shown in the modal header. A nil/empty list yields no
// groups, so the counters stay plain, non-clickable numbers.
//
// labels is both the display-name map and the admission list: an insight whose
// Source is absent from it is dropped, and excluded from the returned counts.
func insightDetails(ins []facts.Insight, labels map[string]string) (groups []insightGroup, structural, candidate int) {
	if len(ins) == 0 {
		return nil, 0, 0
	}

	bySource := make(map[string][]insightRow)
	for _, in := range ins {
		if _, known := labels[in.Source]; !known {
			continue // produced by explainers this build does not have
		}
		conf := int(math.Round(in.Confidence * 100))
		isStructural := in.Confidence >= 1.0
		if isStructural {
			structural++
		} else {
			candidate++
		}
		bySource[in.Source] = append(bySource[in.Source], insightRow{
			Title:      in.Title,
			Confidence: conf,
			Structural: isStructural,
			Evidence:   firstEvidence(in.Evidence),
		})
	}

	maxCount := 1
	for _, items := range bySource {
		if len(items) > maxCount {
			maxCount = len(items)
		}
	}

	for source, items := range bySource {
		sort.Slice(items, func(i, j int) bool {
			if items[i].Confidence != items[j].Confidence {
				return items[i].Confidence > items[j].Confidence
			}
			return items[i].Title < items[j].Title
		})
		groups = append(groups, insightGroup{
			Source: source,
			Label:  labels[source],
			Count:  len(items),
			BarPct: int(math.Round(float64(len(items)) / float64(maxCount) * 100)),
			Items:  items,
		})
	}

	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Label < groups[j].Label
	})

	return groups, structural, candidate
}
