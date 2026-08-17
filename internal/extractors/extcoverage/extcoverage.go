// Package extcoverage lets any extractor report what it saw and could not
// resolve, in the shape the cross-repo layer already emits.
//
// The mechanism was built for the Ruby extractor and its ADR predicted this
// exact problem: "a zero from an extractor that never adopted it reads the same
// as a zero from one that did." A year of that is nineteen extractors and one
// reporter — so the helper moved out of the Ruby package, which is the whole
// difference between a mechanism and one extractor's private habit.
package extcoverage

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// Fact accounts for what an extractor resolved and what it could not, keyed by
// a named cause so the number is a task rather than a total.
//
// It returns false when the extractor had nothing to look at. An extractor that
// examined nothing must not report a confident zero — that is the failure this
// whole mechanism exists to prevent, and reproducing it one level down would be
// its own joke.
func Fact(repoPath, name, edgeType string, resolved int, unresolved map[string]int) (facts.Fact, bool) {
	total := 0
	for _, n := range unresolved {
		total += n
	}
	if resolved == 0 && total == 0 {
		return facts.Fact{}, false
	}

	causes := make([]string, 0, len(unresolved))
	for cause := range unresolved {
		causes = append(causes, cause)
	}
	sort.Strings(causes)

	entry := map[string]any{
		"edge_type":  edgeType,
		"detected":   resolved + total,
		"resolved":   resolved,
		"unresolved": total,
	}
	props := map[string]any{
		"extractor":     extractorOf(name),
		"language":      extractorOf(name),
		"edge_coverage": []map[string]any{entry},
	}
	if len(causes) > 0 {
		counts := make([]string, 0, len(causes))
		for _, cause := range causes {
			counts = append(counts, fmt.Sprintf("%s=%d", cause, unresolved[cause]))
		}
		// Naming what was unread is what makes the number actionable: "59 unread
		// declarations" is a metric, "jsonapi_resources=59" is a task.
		props["unresolved_macros"] = strings.Join(counts, ",")
	}
	return facts.Fact{
		Kind:  facts.KindExtraction,
		Name:  name,
		File:  filepath.Base(repoPath),
		Props: props,
	}, true
}

// extractorOf reads the extractor's name off the fact name, which is written
// "<extractor>:<surface>" — "ruby:routes", "typescript:templates".
func extractorOf(name string) string {
	if extractor, _, found := strings.Cut(name, ":"); found {
		return extractor
	}
	return name
}
