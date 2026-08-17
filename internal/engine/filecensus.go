package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/plugin"
)

// censusCauseCap bounds the aggregated skip causes retained on the receipt,
// matching the other receipt samples: the census leads with the biggest holes
// and states how many causes it did not list by leaving the counts to sum.
const censusCauseCap = 10

// noExtensionKind is the ExcludedKinds key for a file with no extension, so
// Makefiles and dotless scripts are named rather than folded into "".
const noExtensionKind = "(no extension)"

// fileCensus places every file the walker visited in exactly one bucket:
// parsed (it produced at least one fact), excluded-by-ignore (an ignore glob
// dropped it), excluded-by-kind (no enabled extractor declares ownership of
// its path — the extension is recorded, so the receipt names the vocabulary
// gap), or skipped-with-cause (an extractor claimed it and it still produced
// nothing — the parseable-unparsed set the census exists to surface).
//
// The cause is the most specific thing the run recorded: a parse error naming
// the file, then a claiming extractor that never ran (not detected, disabled
// by detection, or failed), then the residual "claimed the file, emitted no
// fact". Ownership comes from plugin.FileOwner because it is the only claim an
// extractor states about paths; an extractor probing content instead claims no
// extension, and its unparsed candidates land in excluded-by-kind — a stated
// limit of the account, not a silent one, since the extension is still named.
//
// prefix is the repo-label prefix append mode stamps on fact files. ranNames
// is the extractor set that actually contributed this run (fresh or cached).
// Everything is sorted before it lands on the receipt, so the census is a
// function of the walk and never of map order.
func (e *Engine) fileCensus(files []string, prefix string, skips walkSkips, ranNames []string, parseErrs []facts.ParseError) *facts.FileCensus {
	census := &facts.FileCensus{
		FilesWalked:      len(files) + skips.count,
		ExcludedByIgnore: skips.count,
	}

	type owner struct {
		name string
		fo   plugin.FileOwner
	}
	var owners []owner
	for _, ext := range e.extractors.All() {
		if !e.cfg.IsExtractorEnabled(ext.Name()) {
			continue
		}
		if fo, ok := ext.(plugin.FileOwner); ok {
			owners = append(owners, owner{name: ext.Name(), fo: fo})
		}
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].name < owners[j].name })

	ran := make(map[string]bool, len(ranNames))
	for _, name := range ranNames {
		ran[name] = true
	}
	errByFile := map[string]facts.ParseError{}
	for _, pe := range parseErrs {
		if pe.File == "" {
			continue
		}
		if _, seen := errByFile[pe.File]; !seen {
			errByFile[pe.File] = pe
		}
	}

	parsed := e.store.FilesWithFacts(files, prefix)
	kinds := map[string]int{}
	causes := map[string]int{}
	for _, f := range files {
		if parsed[f] {
			census.Parsed++
			continue
		}
		rel := filepath.ToSlash(f)
		var claimants []string
		for _, o := range owners {
			if o.fo.OwnsFile(rel) {
				claimants = append(claimants, o.name)
			}
		}
		if len(claimants) == 0 {
			census.ExcludedByKind++
			kind := strings.ToLower(filepath.Ext(rel))
			if kind == "" {
				kind = noExtensionKind
			}
			kinds[kind]++
			continue
		}
		census.SkippedWithCause++
		causes[skipCause(rel, claimants, ran, errByFile)]++
	}

	if len(kinds) > 0 {
		census.ExcludedKinds = kinds
	}
	census.TopSkipCauses = topCauses(causes)
	return census
}

// skipCause names why a claimed file produced no fact, most specific first.
func skipCause(rel string, claimants []string, ran map[string]bool, errByFile map[string]facts.ParseError) string {
	if pe, ok := errByFile[rel]; ok {
		return fmt.Sprintf("%s: %s", pe.Extractor, pe.Msg)
	}
	var idle []string
	for _, name := range claimants {
		if !ran[name] {
			idle = append(idle, name)
		}
	}
	if len(idle) == len(claimants) {
		return fmt.Sprintf("claimed by %s, which did not run this snapshot", strings.Join(idle, ", "))
	}
	return fmt.Sprintf("claimed by %s, no facts emitted", strings.Join(claimants, ", "))
}

// topCauses aggregates the cause tallies into the capped, deterministically
// ordered list the receipt carries: count descending, cause ascending.
func topCauses(causes map[string]int) []facts.CensusCause {
	out := make([]facts.CensusCause, 0, len(causes))
	for cause, count := range causes {
		out = append(out, facts.CensusCause{Cause: cause, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Cause < out[j].Cause
	})
	if len(out) > censusCauseCap {
		out = out[:censusCauseCap]
	}
	return out
}
