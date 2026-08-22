package constraints

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func modelMethod(name string, exported bool) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: "app/models/order.rb", Repo: "app",
		Props: map[string]any{"symbol_kind": "method", "language": "ruby", "exported": exported}}
}

// forbid_name is require_name read the other way: a member whose name
// matches the bounded pattern is the breach. surface: exported leaves the
// private helper alone, and a member outside the pattern is never named.
func TestForbidName_MatchingMembersBreachAndSurfaceNarrows(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("models", map[string]string{"symbol_kind": "method"}, nil),
		formRuleIntent("no-getter-prefixes", map[string]any{"forbid_name": "models", "pattern": "get_*", "surface": "exported"}),
		modelMethod("Order#get_total", true),
		modelMethod("Order#get_lines", false),
		modelMethod("Order#total", true),
	)
	got, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, i := range got {
		if strings.Contains(i.Title, "no-getter-prefixes") {
			titles = append(titles, i.Title)
		}
	}
	if len(titles) != 1 || !strings.Contains(titles[0], "Order#get_total matches the forbidden") {
		t.Fatalf("want exactly the exported getter named, got %v", titles)
	}
}
