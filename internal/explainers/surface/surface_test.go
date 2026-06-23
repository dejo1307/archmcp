package surface

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// addModuleSymbols adds `total` symbols under module mod, the first `exported`
// of which are marked exported=true.
func addModuleSymbols(s *facts.Store, mod string, total, exported int) {
	for i := 0; i < total; i++ {
		s.Add(facts.Fact{
			Kind:  facts.KindSymbol,
			Name:  fmt.Sprintf("%s.Sym%d", mod, i),
			File:  mod + "/file.go",
			Props: map[string]any{"exported": i < exported},
		})
	}
}

func TestExplain_LargePublicSurface(t *testing.T) {
	s := facts.NewStore()
	addModuleSymbols(s, "pkg/leaky", 20, 19) // 95% exported, above floor and ratio
	addModuleSymbols(s, "pkg/tight", 20, 4)  // well encapsulated

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 1 {
		t.Fatalf("expected 1 insight, got %d: %+v", len(insights), insights)
	}
	if !strings.Contains(insights[0].Title, "pkg/leaky") {
		t.Errorf("title %q should name the over-exposed module", insights[0].Title)
	}
	if len(insights[0].Evidence) > maxEvidence {
		t.Errorf("evidence not capped: got %d", len(insights[0].Evidence))
	}
}

func TestExplain_BelowRatio(t *testing.T) {
	s := facts.NewStore()
	addModuleSymbols(s, "pkg/mid", 20, 15) // 75% exported, below minExportedRatio

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights below the ratio gate, got %d", len(insights))
	}
}

func TestExplain_SmallModuleExempt(t *testing.T) {
	s := facts.NewStore()
	addModuleSymbols(s, "pkg/tiny", 10, 10) // all exported but below minSymbols

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights for a small module, got %d", len(insights))
	}
}

func TestExplain_MockAndGeneratedExcluded(t *testing.T) {
	s := facts.NewStore()
	addModuleSymbols(s, "internal/mocks", 30, 30)
	addModuleSymbols(s, "pkg/db/generated", 30, 30)
	addModuleSymbols(s, "app/testing/support", 30, 30)

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("mock/generated/testing modules should be excluded, got %d insights", len(insights))
	}
}

func TestExplain_CappedAndRankedBySurface(t *testing.T) {
	s := facts.NewStore()
	// More qualifying modules than the cap; larger surfaces must come first.
	for i := 0; i < maxInsights+5; i++ {
		total := 20 + i // strictly increasing surface size
		addModuleSymbols(s, fmt.Sprintf("pkg/m%02d", i), total, total)
	}

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != maxInsights {
		t.Fatalf("expected output capped at %d, got %d", maxInsights, len(insights))
	}
	// The largest module (m24, total 44) should be reported first.
	if !strings.Contains(insights[0].Title, "pkg/m24") {
		t.Errorf("largest surface should rank first, got %q", insights[0].Title)
	}
}

func TestExplain_MissingVisibilityIgnored(t *testing.T) {
	s := facts.NewStore()
	for i := 0; i < 20; i++ {
		s.Add(facts.Fact{Kind: facts.KindSymbol, Name: fmt.Sprintf("pkg/x.S%d", i), File: "pkg/x/f.go"})
	}

	insights, err := New().Explain(context.Background(), s)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if len(insights) != 0 {
		t.Errorf("expected 0 insights when visibility is unknown, got %d", len(insights))
	}
}
