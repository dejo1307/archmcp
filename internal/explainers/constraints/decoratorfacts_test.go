package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func symbolComponentIntent(name, match string) facts.Fact {
	f := componentIntent(name, match)
	f.Props["kind"] = "symbol"
	return f
}

func TestExplain_RequireRuleVerdictsOverDecoratorFacts(t *testing.T) {
	getter := func(name, file string, props map[string]any) facts.Fact {
		return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Props: props}
	}
	store := facts.NewStore()
	store.Add(
		symbolComponentIntent("component-getters", "app/components/**"),
		ruleIntentProps("expensive-getters-carry-cached", map[string]any{
			"require": "component-getters", "when_prop": "symbol_kind", "when_value": "getter",
			"must_prop": "decorators", "must_value": "cached",
			"mode": "advisory", "because": "mined: 106/10283 getters carry @cached; the boundary lives in evidence and exemptions"}),
		getter("app/components.Badge.tone", "app/components/badge.gts", map[string]any{
			"symbol_kind": "getter", "decorators": "cached tracked", "getter_calls": 4, "language": "typescript"}),
		getter("app/components.BookCard.summary", "app/components/book-card.gts", map[string]any{
			"symbol_kind": "getter", "getter_calls": 6, "language": "typescript"}),
		getter("app/components.BookCard.submit", "app/components/book-card.gts", map[string]any{
			"symbol_kind": "method", "decorators": "action", "language": "typescript"}),
		getter("app/components.LegacyCard", "app/components/legacy-card.hbs", map[string]any{
			"language": "handlebars"}),
	)
	insights, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	violations := withTitlePrefix(insights, "Advisory constraint expensive-getters-carry-cached violated")
	if len(violations) != 1 {
		t.Fatalf("violations = %+v, want exactly the undecorated expensive getter", insights)
	}
	v := violations[0]
	if v.Title != "Advisory constraint expensive-getters-carry-cached violated: app/components.BookCard.summary must have decorators containing cached" {
		t.Errorf("title = %q", v.Title)
	}
	if v.Confidence != advisoryConfidence {
		t.Errorf("confidence = %v, want the advisory %v", v.Confidence, advisoryConfidence)
	}
	if !strings.Contains(v.Description, "Because: mined: 106/10283 getters carry @cached") {
		t.Errorf("description must carry the mined rationale: %q", v.Description)
	}
	for _, in := range insights {
		if strings.Contains(in.Title, "Badge.tone") {
			t.Errorf("the decorated getter must stay silent, got %q", in.Title)
		}
		if strings.Contains(in.Title, "LegacyCard") {
			t.Errorf("a member whose symbol_kind was never measured is out of scope, not in breach: %q", in.Title)
		}
		if strings.Contains(in.Title, "BookCard.submit") {
			t.Errorf("a method is outside the when clause, got %q", in.Title)
		}
	}
}
