package engine

import "github.com/enola-labs/enola/internal/facts"

// positionInsights gives every piece of evidence the span of the fact it
// names, so a reader can show the offending line without each explainer
// carrying four more fields to every construction site.
//
// The span is copied, never derived: evidence is positioned only when the
// store holds a fact with that exact name in that exact file and the fact's
// extractor measured a position. Evidence naming no symbol, a symbol the
// store does not hold, or a fact from an extractor with no spans stays
// unpositioned, and the renderer says so by printing no frame.
func positionInsights(insights []facts.Insight, store *facts.Store) {
	if len(insights) == 0 {
		return
	}
	type key struct{ file, name string }
	positions := map[key]facts.Fact{}
	for _, f := range store.All() {
		if f.Line == 0 || f.Name == "" {
			continue
		}
		k := key{f.File, f.Name}
		if existing, ok := positions[k]; ok && existing.Line <= f.Line {
			continue
		}
		positions[k] = f
	}
	for i := range insights {
		for j := range insights[i].Evidence {
			ev := &insights[i].Evidence[j]
			if ev.Line != 0 || ev.Symbol == "" {
				continue
			}
			f, ok := positions[key{ev.File, ev.Symbol}]
			if !ok {
				continue
			}
			ev.Line, ev.EndLine, ev.Column, ev.EndColumn = f.Line, f.EndLine, f.Column, f.EndColumn
		}
	}
}
