package constraints

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func conceptContractStore() *facts.Store {
	store := facts.NewStore()
	store.Add(
		predicateComponentIntent("view-components", map[string]string{"superclass": "ViewComponent::Base"}, nil),
		formRuleIntent("components-are-suffixed", map[string]any{
			"require_name": "view-components", "pattern": "*Component"}),
		viewComponent("Hires::Cover", "app/components/hires/cover.rb"),
	)
	return store
}

// The pre-edit contract is the surface an agent hits with a changed-file list,
// and a where-only component reached pathInComponent's false arm: with no
// service and no match the conjunction short-circuited, so `plan --paths`
// omitted every rule written in the new vocabulary while `plan --symbols`
// included it.
func TestContractFor_PredicateComponentBindsTheFileItsMemberSitsIn(t *testing.T) {
	bindings, declared := ContractFor(conceptContractStore(), "app/components/hires/cover.rb")
	if !declared {
		t.Fatal("components are declared, ContractFor must say so")
	}
	if len(bindings) != 1 || bindings[0].Component != "view-components" {
		t.Fatalf("bindings = %+v, want the concept component bound to the path its member sits in", bindings)
	}
	if len(bindings[0].Rules) != 1 || bindings[0].Rules[0].Rule != "components-are-suffixed" {
		t.Errorf("rules = %+v, want the rule stated in the new vocabulary", bindings[0].Rules)
	}
}

// And the fail-closed half, unchanged: nothing has been measured about a file
// nobody has written, so a predicate cannot answer for it. A guessed contract
// is worse than none.
func TestContractFor_PredicateComponentRefusesAnUnwrittenPath(t *testing.T) {
	bindings, declared := ContractFor(conceptContractStore(), "app/components/hires/new_thing.rb")
	if !declared {
		t.Fatal("components are declared, ContractFor must say so")
	}
	if len(bindings) != 0 {
		t.Fatalf("bindings = %+v, want none: no fact in that file has demonstrated the predicate", bindings)
	}
}

// The same component reached by fact name binds identically — the two entry
// points to one contract must not disagree.
func TestContractFor_PredicateComponentBindsByFactName(t *testing.T) {
	bindings, _ := ContractFor(conceptContractStore(), "Hires::Cover")
	if len(bindings) != 1 || bindings[0].Component != "view-components" {
		t.Fatalf("bindings = %+v, want the concept component", bindings)
	}
}

// A path component keeps answering for a file nobody has written yet — the
// property the predicate arm must not be allowed to take away.
func TestContractFor_PathComponentStillAnswersForAnUnwrittenPath(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		componentIntent("adapters", "app/adapters/**"),
		formRuleIntent("adapters-are-named", map[string]any{"require_name": "adapters", "pattern": "*Adapter"}),
	)
	bindings, declared := ContractFor(store, "app/adapters/grpc/server.go")
	if !declared || len(bindings) != 1 || bindings[0].Component != "adapters" {
		t.Fatalf("bindings = %+v (declared=%v), want the path component to bind before the file exists", bindings, declared)
	}
}
