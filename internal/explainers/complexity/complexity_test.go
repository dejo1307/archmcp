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

// TestExplain_CollapsesConditionalDuplicates asserts a complex method declared once
// per #if/#else branch (two same-name conditional facts) is flagged once, not twice,
// and does not enter the outlier distribution twice. GAP-SW-10.
func TestExplain_CollapsesConditionalDuplicates(t *testing.T) {
	s := facts.NewStore()
	for i := 0; i < 20; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.simple%d", i), 2))
	}
	for _, line := range []int{5, 40} {
		s.Add(facts.Fact{
			Kind:  facts.KindSymbol,
			Name:  "pkg/x.Monster",
			File:  "pkg/x/monster.swift",
			Line:  line,
			Props: map[string]any{"symbol_kind": facts.SymbolMethod, "cyclomatic": 30, "conditional": true},
		})
	}

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	count := 0
	for _, in := range insights {
		if strings.Contains(in.Title, "pkg/x.Monster") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("conditional duplicate flagged %d times, want 1: %+v", count, insights)
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

// TestExplain_MinComplexityBoundary: a function at exactly minComplexity that is
// also a statistical outlier is reported.
func TestExplain_MinComplexityBoundary(t *testing.T) {
	s := facts.NewStore()
	for i := 0; i < 20; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.simple%d", i), 2))
	}
	s.Add(fn("pkg/x.Edge", minComplexity)) // exactly the floor, and an outlier vs the 2s

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 || !strings.Contains(insights[0].Title, "pkg/x.Edge") {
		t.Errorf("complexity == minComplexity (%d) outlier should be reported, got %+v", minComplexity, titlesOf(insights))
	}
}

// TestExplain_Int64PropForm: intProp accepts the int64 shape too.
func TestExplain_Int64PropForm(t *testing.T) {
	s := facts.NewStore()
	for i := 0; i < 20; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.simple%d", i), int64(2)))
	}
	s.Add(fn("pkg/x.Monster", int64(30)))

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight with int64 props, got %d", len(insights))
	}
}

// TestExplain_MissingSymbolKindCounted locks the documented behavior: a symbol
// carrying cyclomatic but no symbol_kind is allowed through (so a missing prop
// never silently drops a real function).
func TestExplain_MissingSymbolKindCounted(t *testing.T) {
	s := facts.NewStore()
	for i := 0; i < 20; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.simple%d", i), 2))
	}
	// No symbol_kind prop, but has cyclomatic.
	s.Add(facts.Fact{Kind: facts.KindSymbol, Name: "pkg/x.NoKind", File: "pkg/x/f.go",
		Props: map[string]any{"cyclomatic": 30}})

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 || !strings.Contains(insights[0].Title, "pkg/x.NoKind") {
		t.Errorf("symbol with cyclomatic but no kind should be counted, got %+v", titlesOf(insights))
	}
}

// TestExplain_CapsAtMaxInsightsMostComplexFirst: more outliers than the cap are
// trimmed to maxInsights, deepest complexity first.
func TestExplain_CapsAtMaxInsightsMostComplexFirst(t *testing.T) {
	s := facts.NewStore()
	// A large simple baseline keeps the mean+2σ threshold well below the outliers,
	// so all 16 (> maxInsights) qualify and exercise the cap.
	for i := 0; i < 200; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.simple%d", i), 1))
	}
	// 16 distinct outliers with complexity 40..55.
	for i := 0; i < 16; i++ {
		s.Add(fn(fmt.Sprintf("pkg/x.M%02d", i), 40+i))
	}

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != maxInsights {
		t.Fatalf("expected output capped at %d, got %d", maxInsights, len(insights))
	}
	if !strings.Contains(insights[0].Title, "(55)") {
		t.Errorf("most-complex (55) should rank first, got %q", insights[0].Title)
	}
}

func titlesOf(ins []facts.Insight) []string {
	out := make([]string, len(ins))
	for i, in := range ins {
		out[i] = in.Title
	}
	return out
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
