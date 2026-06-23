package complexity

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func fn(name string, cyclo any) facts.Fact {
	return facts.Fact{
		Kind:  facts.KindSymbol,
		Name:  name,
		File:  "pkg/x/f.go",
		Line:  10,
		Props: map[string]any{"symbol_kind": facts.SymbolFunc, "cyclomatic": cyclo},
	}
}

func TestExplain_DetectsOutlier(t *testing.T) {
	s := facts.NewStore()
	// A cluster of simple functions plus one very complex one.
	for i := 0; i < 20; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.simple%d", i), 2))
	}
	s.Add(fn("pkg/x.Monster", 30))

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 complexity insight, got %d: %+v", len(insights), insights)
	}
	if !strings.Contains(insights[0].Title, "pkg/x.Monster") {
		t.Errorf("title %q should name the complex function", insights[0].Title)
	}
}

func TestExplain_FloatPropFromJSONL(t *testing.T) {
	// Simulate a snapshot reloaded from JSONL where numbers decode as float64.
	s := facts.NewStore()
	for i := 0; i < 20; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.simple%d", i), float64(2)))
	}
	s.Add(fn("pkg/x.Monster", float64(30)))

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight with float64 props, got %d", len(insights))
	}
}

func TestExplain_BelowFloor(t *testing.T) {
	s := facts.NewStore()
	// One function stands out statistically but is still below minComplexity.
	for i := 0; i < 20; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.simple%d", i), 1))
	}
	s.Add(fn("pkg/x.Slightly", 6))

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights below complexity floor, got %d", len(insights))
	}
}

func TestExplain_IgnoresNonCallable(t *testing.T) {
	s := facts.NewStore()
	for i := 0; i < 20; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.simple%d", i), 2))
	}
	// A struct with a (nonsensical) high cyclomatic prop must be ignored.
	s.Add(facts.Fact{
		Kind:  facts.KindSymbol,
		Name:  "pkg/x.BigStruct",
		Props: map[string]any{"symbol_kind": facts.SymbolStruct, "cyclomatic": 50},
	})

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected non-callable symbols to be ignored, got %d insights", len(insights))
	}
}
