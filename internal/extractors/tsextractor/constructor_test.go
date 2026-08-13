package tsextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// A constructor runs on every instantiation, so what it calls is the whole of
// "this class fetches when it is built" — the convention a style guide states
// and nothing could enforce while the member walk skipped it.
func TestExtract_ConstructorIsAFactCarryingItsCalls(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/panel.ts": `import { fetchJobs } from './api';

export class Panel {
  constructor() {
    fetchJobs();
  }

  render() {
    return null;
  }
}`,
	}, false)

	f, ok := findFact(ff, "src.Panel.constructor")
	if !ok {
		t.Fatal("expected a fact for src.Panel.constructor")
	}
	if f.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("symbol_kind = %v, want method", f.Props["symbol_kind"])
	}
	if f.Props["receiver"] != "Panel" {
		t.Errorf("receiver = %v, want Panel", f.Props["receiver"])
	}
	var called bool
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls {
			called = true
		}
	}
	if !called {
		t.Error("the constructor carries no calls relation; the rule this fact exists for reads exactly that")
	}
	if _, ok := findFact(ff, "src.Panel.render"); !ok {
		t.Error("an ordinary method stopped being emitted alongside the constructor")
	}
}

// The other half of the condition that hid the constructor. A #-private member
// is private in the language's own sense and has no callers to measure, so it
// stays skipped — separating the two must not have widened the walk.
func TestExtract_PrivateHashMemberStaysSkipped(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/vault.ts": `export class Vault {
  #secret() {
    return 1;
  }

  open() {
    return 2;
  }
}`,
	}, false)

	if _, ok := findFact(ff, "src.Vault.#secret"); ok {
		t.Error("a #-private member became a fact; only the constructor was meant to")
	}
	if _, ok := findFact(ff, "src.Vault.open"); !ok {
		t.Error("the ordinary method is missing, so the walk itself broke")
	}
}
