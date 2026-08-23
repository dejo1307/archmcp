package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// Census is the verdict's account of what the run could not see: the
// snapshot's own unseen census plus the one cause only the verdict can judge,
// ledger entries that matched no finding. It is carried on every outcome,
// PASS included, so a reader can tell a graph that resolved the change from
// one that skipped the files it touched.
type Census struct {
	// Recorded is false when the snapshot predates the census: the line then
	// says so instead of reading as nothing to see.
	Recorded              bool                 `json:"recorded"`
	FilesExcludedByIgnore int                  `json:"files_excluded_by_ignore"`
	DirsExcludedByIgnore  int                  `json:"dirs_excluded_by_ignore"`
	ProviderSkips         []facts.ProviderSkip `json:"provider_skips,omitempty"`
	OutsideGraph          map[string]int       `json:"outside_graph,omitempty"`
	DeadExemptions        int                  `json:"dead_exemptions"`
	UnusedSuppressions    int                  `json:"unused_suppressions"`
	DynamicFeatureClasses int                  `json:"dynamic_feature_classes"`
	// ProviderOverlap is each provider's repeated, respelled and conflicting
	// relation counts summed over kinds, from the receipt.
	ProviderOverlap []ProviderOverlapLine `json:"provider_overlap,omitempty"`
}

// ProviderOverlapLine is one provider's overlap with the extractor, summed
// over relation kinds for the line; the per-kind detail stays in the receipt.
type ProviderOverlapLine struct {
	Name            string `json:"name"`
	AlreadyResolved int    `json:"already_resolved"`
	Respelled       int    `json:"respelled"`
	Conflict        int    `json:"conflict"`
}

// AttachCensus fills the verdict's census from the current snapshot's receipt
// and the policy's ledger. Unused suppressions are ledger entries that
// suppressed no finding in the current snapshot: the one cause that needs the
// verdict's own view, since the engine never reads the ledger.
func AttachCensus(v Verdict, meta facts.SnapshotMeta, p Policy, currentFindings []facts.Insight) Verdict {
	c := &Census{OutsideGraph: map[string]int{}}
	if u := meta.Unseen; u != nil {
		c.Recorded = true
		c.FilesExcludedByIgnore = u.FilesExcludedByIgnore
		c.DirsExcludedByIgnore = u.DirsExcludedByIgnore
		c.ProviderSkips = u.ProviderSkips
		for k, n := range u.OutsideGraph {
			c.OutsideGraph[k] = n
		}
		c.DeadExemptions = u.DeadExemptions
		c.DynamicFeatureClasses = u.DynamicFeatureClasses
	}
	for _, r := range meta.Providers {
		if r.Skipped || len(r.Overlap) == 0 {
			continue
		}
		line := ProviderOverlapLine{Name: r.Name}
		for _, o := range r.Overlap {
			line.AlreadyResolved += o.AlreadyResolved
			line.Respelled += o.Respelled
			line.Conflict += o.Conflict
		}
		c.ProviderOverlap = append(c.ProviderOverlap, line)
	}
	for _, s := range p.Suppressions {
		used := false
		for _, in := range currentFindings {
			if s.suppresses(in) {
				used = true
				break
			}
		}
		if !used {
			c.UnusedSuppressions++
		}
	}
	v.Census = c
	return v
}

// Line renders the census as the one line the verdict prints under its
// headline: every cause above zero, in a fixed order, or "nothing" when the
// run saw everything it was asked to. Counts the line points at but does not
// carry (receiver resolution, bare-name calls) live in the coverage report.
func (c *Census) Line() string {
	if c == nil || !c.Recorded {
		return "could not see: not recorded (snapshot predates the census)"
	}
	var parts []string
	if c.FilesExcludedByIgnore > 0 || c.DirsExcludedByIgnore > 0 {
		parts = append(parts, fmt.Sprintf("%s and %s excluded by ignore globs",
			plural(c.FilesExcludedByIgnore, "file", "files"), plural(c.DirsExcludedByIgnore, "directory", "directories")))
	}
	for _, s := range c.ProviderSkips {
		if s.Reason != "" {
			parts = append(parts, fmt.Sprintf("%s skipped (%s)", s.Name, s.Reason))
			continue
		}
		var causes []string
		for _, cause := range s.Causes {
			causes = append(causes, fmt.Sprintf("%d %s", cause.Count, cause.Cause))
		}
		if len(causes) > 0 {
			parts = append(parts, fmt.Sprintf("%s: %s", s.Name, strings.Join(causes, ", ")))
		}
	}
	kinds := make([]string, 0, len(c.OutsideGraph))
	for k, n := range c.OutsideGraph {
		if n > 0 {
			kinds = append(kinds, k)
		}
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		parts = append(parts, fmt.Sprintf("%d %s targets outside the graph", c.OutsideGraph[k], k))
	}
	if c.DeadExemptions > 0 {
		parts = append(parts, plural(c.DeadExemptions, "exemption matching nothing", "exemptions matching nothing"))
	}
	if c.UnusedSuppressions > 0 {
		parts = append(parts, plural(c.UnusedSuppressions, "unused suppression", "unused suppressions"))
	}
	for _, o := range c.ProviderOverlap {
		if o.Conflict > 0 || o.Respelled > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d relations contradict the extractor, %d respelled, %d repeated", o.Name, o.Conflict, o.Respelled, o.AlreadyResolved))
		}
	}
	if c.DynamicFeatureClasses > 0 {
		parts = append(parts, plural(c.DynamicFeatureClasses, "class carrying a dynamic dispatch", "classes carrying a dynamic dispatch"))
	}
	if len(parts) == 0 {
		return "could not see: nothing"
	}
	return "could not see: " + strings.Join(parts, "; ")
}
