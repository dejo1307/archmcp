package mixinowner

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func symbol(name, kind string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: "app/x.rb", Props: map[string]any{"symbol_kind": kind, "language": "ruby"}}
}

func mixin(includer, module, kind string) facts.Fact {
	return facts.Fact{Kind: facts.KindDependency, Name: includer + " -> " + module, File: "app/x.rb",
		Props: map[string]any{"language": "ruby", mixinKindProp: kind}, Relations: []facts.Relation{{Kind: facts.RelImplements, Target: module}}}
}

func hasMethods(store *facts.Store, name string) (map[string]bool, map[string]any) {
	for _, f := range store.ByKind(facts.KindSymbol) {
		if f.Name != name {
			continue
		}
		out := map[string]bool{}
		for _, r := range f.Relations {
			if r.Kind == facts.RelHasMethod {
				out[r.Target] = true
			}
		}
		members, _ := f.Props[MembersProp].(map[string]any)
		return out, members
	}
	return nil, nil
}

// A class that includes a module owns the module's methods, with the mixin kind
// recorded per member; the module keeps them too, and a nested module's members
// stay the nested module's.
func TestBind_IncluderOwnsTheModulesMembersWithProvenance(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		symbol("Chargeable", facts.SymbolInterface),
		symbol("Chargeable#charge", facts.SymbolMethod),
		symbol("Chargeable.fee_for", facts.SymbolMethod),
		symbol("Chargeable::Ledger", facts.SymbolInterface),
		symbol("Chargeable::Ledger#post", facts.SymbolMethod),
		symbol("Order", facts.SymbolClass),
		symbol("Order#total", facts.SymbolMethod),
		mixin("Order", "Chargeable", "include"),
	)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	owned, members := hasMethods(store, "Order")
	if !owned["Chargeable#charge"] || !owned["Chargeable.fee_for"] {
		t.Fatalf("Order does not own the mixed-in members: %v", owned)
	}
	if owned["Chargeable::Ledger#post"] {
		t.Fatal("a nested module's member was projected onto the includer")
	}
	if members["Chargeable#charge"] != "include" {
		t.Fatalf("provenance missing: %v", members)
	}
}

// A mixin naming no module fact projects nothing rather than guessing, and a
// second bind does not duplicate relations.
func TestBind_UnknownModuleProjectsNothingAndBindIsIdempotent(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		symbol("Billing", facts.SymbolInterface),
		symbol("Billing#invoice", facts.SymbolMethod),
		symbol("Order", facts.SymbolClass),
		mixin("Order", "Billing", "extend"),
		mixin("Order", "Vanished", "include"),
	)
	for i := 0; i < 2; i++ {
		if err := New().Bind(context.Background(), store); err != nil {
			t.Fatal(err)
		}
	}
	owned, members := hasMethods(store, "Order")
	if len(owned) != 1 || !owned["Billing#invoice"] {
		t.Fatalf("want exactly the Billing member owned once, got %v", owned)
	}
	if members["Billing#invoice"] != "extend" {
		t.Fatalf("provenance: %v", members)
	}
}

// A module named without its namespace resolves the way Ruby resolves a
// constant: inside the includer's namespace first, then outward.
func TestBind_ResolvesTheModuleLexicallyFromTheIncluder(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		symbol("Integration::Cronofy::TokenRefreshable", facts.SymbolInterface),
		symbol("Integration::Cronofy::TokenRefreshable#refresh_token!", facts.SymbolMethod),
		symbol("Integration::Cronofy::EmployeeItem", facts.SymbolClass),
		mixin("Integration::Cronofy::EmployeeItem", "TokenRefreshable", "include"),
		symbol("Onboarding::Stage", facts.SymbolClass),
		mixin("Onboarding::Stage", "Orderable", "prepend"),
	)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	owned, members := hasMethods(store, "Integration::Cronofy::EmployeeItem")
	if !owned["Integration::Cronofy::TokenRefreshable#refresh_token!"] || members["Integration::Cronofy::TokenRefreshable#refresh_token!"] != "include" {
		t.Fatalf("lexical resolution failed: %v %v", owned, members)
	}
	if owned, _ := hasMethods(store, "Onboarding::Stage"); len(owned) != 0 {
		t.Fatalf("Orderable names no module fact yet projected %v", owned)
	}
}
