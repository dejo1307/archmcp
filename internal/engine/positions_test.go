package engine

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// Evidence is positioned from the fact it names, and only from that fact: a
// symbol the store does not hold, a fact with no measured line, and evidence
// that already carries a position are all left alone.
func TestPositionInsights_CopiesTheCitedFactsSpan(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "Order#get_total", File: "app/models/order.rb", Line: 2, EndLine: 4, Column: 3, EndColumn: 6},
		facts.Fact{Kind: facts.KindSymbol, Name: "Order", File: "app/models/order.rb"},
	)
	insights := []facts.Insight{{Evidence: []facts.Evidence{
		{File: "app/models/order.rb", Symbol: "Order#get_total"},
		{File: "app/models/order.rb", Symbol: "Order"},
		{File: "app/models/order.rb", Symbol: "Ghost"},
		{File: "app/models/order.rb", Symbol: "Order#get_total", Line: 99},
	}}}
	positionInsights(insights, store)
	got := insights[0].Evidence
	if got[0].Line != 2 || got[0].EndLine != 4 || got[0].Column != 3 || got[0].EndColumn != 6 {
		t.Fatalf("cited fact's span not copied: %+v", got[0])
	}
	for i, ev := range got[1:3] {
		if ev.Line != 0 {
			t.Fatalf("evidence %d must stay unpositioned: %+v", i+1, ev)
		}
	}
	if got[3].Line != 99 {
		t.Fatal("an explainer's own position is not overwritten")
	}
}
