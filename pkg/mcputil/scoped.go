package mcputil

import (
	"fmt"
	"strings"
)

// Scoped pairs a filtered result set with the SIZE of the population it was drawn
// from — never with the population itself.
//
// It exists to make one class of bug unrepresentable. A tool that filters its
// results and then renders a summary has two sets in scope, and the summary must be
// computed from the filtered one. `analyze_performance` computed it from the other:
// its summary struct was built before the filter ran and rendered beside the
// filtered list, so `package="androidTest"` printed "Performance: 61 findings" —
// the repo-wide total — above an empty table. Nothing in the output revealed it, and
// the number was wrong in the most convincing possible way: it was a real number,
// just the answer to a question the caller had not asked.
//
// The OSS server avoids this by construction rather than by care: renderInsightsSummary
// receives only the matched slice and does not have the unfiltered corpus in scope, so
// it *cannot* count the wrong thing. Scoped generalizes that invariant. Population is
// an int, not a slice, so a renderer holding a Scoped has nothing to range over but
// the filtered set.
//
// Use it for any tool surface where a filter and a summary meet.
type Scoped[T any] struct {
	// Items is the filtered set — the only collection a renderer may count.
	Items []T
	// Population is the size of the unfiltered set, carried for context so a zero
	// result can say what it was drawn from. It is deliberately a count, not the
	// data.
	Population int
	// Filter describes the filter in the caller's own terms, e.g. `package="x"`.
	// Empty means no filter was applied.
	Filter string
}

// Scope pairs a filtered set with the size of its population.
func Scope[T any](population int, filtered []T, filter string) Scoped[T] {
	return Scoped[T]{Items: filtered, Population: population, Filter: filter}
}

// Filtered reports whether a filter was applied.
func (s Scoped[T]) Filtered() bool { return s.Filter != "" }

// Headline renders the count sentence, naming the filter when there was one.
//
//	unfiltered → "61 findings."
//	filtered   → `0 findings under package="androidTest" (of 61 repo-wide).`
//
// The filtered form always states the population, because a bare "0 findings" is
// indistinguishable from a filter that silently matched nothing — which is the
// second half of the same defect: analyze_performance's `package=` matched against
// the symbol NAME, so on a Rails repo (whose symbol names carry no path) it returned
// zero for every real package, forever.
func (s Scoped[T]) Headline(noun string) string {
	if !s.Filtered() {
		return fmt.Sprintf("%d %s.", len(s.Items), noun)
	}
	return fmt.Sprintf("%d %s under %s (of %d repo-wide).", len(s.Items), noun, s.Filter, s.Population)
}

// DescribeFilters renders a set of name=value filter pairs into the form Headline
// expects, skipping empties, so every tool describes its filters the same way.
func DescribeFilters(pairs ...string) string {
	if len(pairs)%2 != 0 {
		panic("mcputil.DescribeFilters: odd number of arguments; want name, value pairs")
	}
	var parts []string
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%q", pairs[i], pairs[i+1]))
	}
	return strings.Join(parts, ", ")
}
