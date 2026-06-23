// Package surface flags modules with an oversized public surface: large modules
// that export almost all of their symbols, encapsulating little.
//
// Two things keep this from flooding — which a naive ratio test does, because in
// Go ("Capitalized == public") and Ruby ("public is the default") most packages
// legitimately export the bulk of their symbols:
//
//   - mock/test/generated packages (which export everything) are skipped, and a
//     meaningful size + near-total export ratio is required; and
//   - only the worst offenders are reported (top N by public-surface size), so a
//     large repo surfaces a digestible shortlist rather than hundreds of hits.
//
// It is a heuristic shortlist of modules worth a visibility review, not a list of
// definite defects.
package surface

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/explainers/common"
	"github.com/enola-labs/enola/internal/facts"
)

const (
	// minSymbols is the smallest module (by symbol count) considered; below this,
	// exporting everything is unremarkable.
	minSymbols = 15
	// minExportedRatio is the exported/total ratio at or above which a module
	// counts as near-fully-exported.
	minExportedRatio = 0.85
	// maxInsights caps how many modules are reported, largest public surface
	// first, so the output stays a shortlist on big repos.
	maxInsights = 20
	// maxEvidence caps how many exported symbols are listed per insight.
	maxEvidence = 8
)

// excludedSegments are path segments whose modules are skipped: mocks, test
// helpers, fixtures, and generated code naturally export everything and aren't
// hand-maintained public APIs.
var excludedSegments = map[string]bool{
	"mock": true, "mocks": true,
	"test": true, "tests": true, "testing": true,
	"testutil": true, "testutils": true, "testdata": true,
	"fixture": true, "fixtures": true,
	"spec": true, "specs": true,
	"generated": true,
}

// SurfaceExplainer detects modules with an oversized exported surface.
type SurfaceExplainer struct{}

// New creates a new SurfaceExplainer.
func New() *SurfaceExplainer {
	return &SurfaceExplainer{}
}

func (e *SurfaceExplainer) Name() string {
	return "exported-surface"
}

type moduleSurface struct {
	total    int
	exported []string
}

// Explain tallies exported vs total symbols per module and reports the modules
// with the largest near-fully-exported public surface.
func (e *SurfaceExplainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	symbols := store.ByKind(facts.KindSymbol)
	if len(symbols) == 0 {
		return nil, nil
	}

	mods := make(map[string]*moduleSurface)
	for _, s := range symbols {
		exported, ok := s.Props["exported"].(bool)
		if !ok {
			// Extractor didn't record visibility; ignore so it doesn't distort the ratio.
			continue
		}
		mod := common.SymbolModule(s.Name)
		ms := mods[mod]
		if ms == nil {
			ms = &moduleSurface{}
			mods[mod] = ms
		}
		ms.total++
		if exported {
			ms.exported = append(ms.exported, s.Name)
		}
	}

	type candidate struct {
		module string
		ms     *moduleSurface
		ratio  float64
	}
	var candidates []candidate
	for mod, ms := range mods {
		if ms.total < minSymbols || excludedModule(mod) {
			continue
		}
		ratio := float64(len(ms.exported)) / float64(ms.total)
		if ratio < minExportedRatio {
			continue
		}
		candidates = append(candidates, candidate{module: mod, ms: ms, ratio: ratio})
	}

	// Largest public surface first (the most worth reviewing), then by ratio,
	// then name — so the top-N cap keeps the most significant modules.
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i].ms.exported) != len(candidates[j].ms.exported) {
			return len(candidates[i].ms.exported) > len(candidates[j].ms.exported)
		}
		if candidates[i].ratio != candidates[j].ratio {
			return candidates[i].ratio > candidates[j].ratio
		}
		return candidates[i].module < candidates[j].module
	})

	var insights []facts.Insight
	for i, c := range candidates {
		if i >= maxInsights {
			break
		}
		exportedCount := len(c.ms.exported)
		sorted := append([]string(nil), c.ms.exported...)
		sort.Strings(sorted)
		evidence := make([]facts.Evidence, 0, maxEvidence)
		for j, sym := range sorted {
			if j >= maxEvidence {
				break
			}
			evidence = append(evidence, facts.Evidence{Symbol: sym, Detail: "exported"})
		}

		insights = append(insights, facts.Insight{
			Title: fmt.Sprintf("Large public surface: %s exports %d of %d symbols (%.0f%%)", c.module, exportedCount, c.ms.total, c.ratio*100),
			Description: fmt.Sprintf(
				"Module %q exports %d of its %d symbols (%.0f%%). A large, near-fully-exported module hides "+
					"little of its implementation, so consumers can couple to internals and it is hard to "+
					"change without breaking them. Worth a visibility review — though a deliberate library "+
					"API may legitimately export most of its surface.",
				c.module, exportedCount, c.ms.total, c.ratio*100,
			),
			Confidence: 0.6,
			Evidence:   evidence,
			Actions: []string{
				"Unexport symbols that are only used within the module",
				"Define a small, intentional public API and hide the rest",
				"Split the module if it has several independent public responsibilities",
			},
		})
	}

	return insights, nil
}

// excludedModule reports whether any path segment of the module is a mock/test/
// generated marker.
func excludedModule(mod string) bool {
	for _, seg := range strings.Split(strings.ToLower(mod), "/") {
		if excludedSegments[seg] {
			return true
		}
	}
	return false
}
