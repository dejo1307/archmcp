package tsextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func classFact(t *testing.T, ff []facts.Fact, name string) facts.Fact {
	t.Helper()
	f, ok := findFact(ff, name)
	if !ok {
		t.Fatalf("no fact named %q; symbols: %v", name, symbolNames(ff))
	}
	if f.Props["symbol_kind"] != facts.SymbolClass {
		t.Fatalf("fact %q is %v, want a class", name, f.Props["symbol_kind"])
	}
	return f
}

func symbolNames(ff []facts.Fact) []string {
	var out []string
	for _, f := range ff {
		if f.Kind == facts.KindSymbol {
			out = append(out, f.Name)
		}
	}
	return out
}

func wantSuperclass(t *testing.T, f facts.Fact, super, module string) {
	t.Helper()
	if got, _ := f.Props[superclassProp].(string); got != super {
		t.Errorf("%s superclass = %q, want %q", f.Name, got, super)
	}
	got, present := f.Props[superclassModuleProp]
	switch {
	case module == "" && present:
		t.Errorf("%s carries superclass_module %q, want none", f.Name, got)
	case module != "" && got != module:
		t.Errorf("%s superclass_module = %v, want %q", f.Name, got, module)
	}
}

// TestSuperclass_SameIdentifierDifferentModules is the reason the module prop
// exists: two files write `extends Controller` and mean unrelated base classes.
// The identifier is what the source says and the module is what tells them
// apart — a rule naming Stimulus controllers must not catch Ember's.
func TestSuperclass_SameIdentifierDifferentModules(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"app/javascript/controllers/dropdown_controller.ts": `import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  toggle() {}
}
`,
		"app/frontend/controllers/jobs.ts": `import Controller from "@ember/controller"

export default class JobsController extends Controller {
}
`,
		"app/frontend/components/card.ts": `import Component from "@glimmer/component"

export default class Card extends Component {
}
`,
		"app/frontend/components/legacy.ts": `import Component from "@ember/component"

export default class Legacy extends Component {
}
`,
	}, false)

	wantSuperclass(t, classFact(t, ff, "app/javascript/controllers.DropdownController"), "Controller", "@hotwired/stimulus")
	wantSuperclass(t, classFact(t, ff, "app/frontend/controllers.JobsController"), "Controller", "@ember/controller")
	wantSuperclass(t, classFact(t, ff, "app/frontend/components.Card"), "Component", "@glimmer/component")
	wantSuperclass(t, classFact(t, ff, "app/frontend/components.Legacy"), "Component", "@ember/component")
}

// TestSuperclass_AnonymousDefaultExportKeepsItsFilenameName: 42 of the 50
// Stimulus controllers in one production frontend are `export default class
// extends Controller`, so the majority case is a class with no name of its own.
// The heritage must survive the filename fallback that names it.
func TestSuperclass_AnonymousDefaultExportKeepsItsFilenameName(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"app/javascript/controllers/modal_controller.js": `import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  open() {}
}
`,
	}, false)

	f := classFact(t, ff, "app/javascript/controllers.ModalController")
	wantSuperclass(t, f, "Controller", "@hotwired/stimulus")
	if _, ok := findFact(ff, "app/javascript/controllers.ModalController.open"); !ok {
		t.Errorf("the anonymous class lost its members: %v", symbolNames(ff))
	}
}

// TestSuperclass_LocalBaseNamesNoModule: a base class the file declares itself,
// or one no import binds, states its name and nothing more. Inventing a module
// for it from the identifier or the path is the guess this prop refuses.
func TestSuperclass_LocalBaseNamesNoModule(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/shapes.ts": `export class Shape {}

export class Circle extends Shape {}

export class Boom extends Error {}
`,
	}, false)

	wantSuperclass(t, classFact(t, ff, "src.Circle"), "Shape", "")
	wantSuperclass(t, classFact(t, ff, "src.Boom"), "Error", "")
	if f := classFact(t, ff, "src.Shape"); f.Props[superclassProp] != nil {
		t.Errorf("a class extending nothing carries superclass %v", f.Props[superclassProp])
	}
}

// TestSuperclass_ResolvedThroughEveryImportForm: the module comes from the
// file's import table, so a relative import resolves to the file it names, an
// aliased one through the alias, and an alias-renamed binding under the name the
// file actually writes in the heritage clause.
func TestSuperclass_ResolvedThroughEveryImportForm(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/base/widget.ts": `export default class Widget {}
export class Panel {}
`,
		"src/ui/button.ts": `import Widget from "../base/widget"

export class Button extends Widget {}
`,
		"src/ui/named.ts": `import { Panel } from "../base/widget"

export class Sidebar extends Panel {}
`,
		"src/ui/aliased.ts": `import { Panel as BasePanel } from "../base/widget"

export class Drawer extends BasePanel {}
`,
	}, false)

	wantSuperclass(t, classFact(t, ff, "src/ui.Button"), "Widget", "src/base/widget")
	wantSuperclass(t, classFact(t, ff, "src/ui.Sidebar"), "Panel", "src/base/widget")
	wantSuperclass(t, classFact(t, ff, "src/ui.Drawer"), "BasePanel", "src/base/widget")
}

// TestSuperclass_GenericBaseIsStillTheBase: `extends Base<T>` names Base. The
// type arguments are types applied to it, not a second reading of the base.
func TestSuperclass_GenericBaseIsStillTheBase(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/store.ts": `import { Collection } from "@acme/data"

export class Jobs extends Collection<Job> {}
`,
	}, false)

	wantSuperclass(t, classFact(t, ff, "src.Jobs"), "Collection", "@acme/data")
}

// TestSuperclass_ComputedHeritageNamesNothing: every form whose base is reached
// through a value rather than written down. Answering from the nearest
// identifier would name the mixin factory, the namespace object or the
// condition — none of which the source states is the base class.
func TestSuperclass_ComputedHeritageNamesNothing(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/forms.ts": `import { mixin } from "@acme/mix"
import * as ns from "@acme/ns"
import { Factory, cond, A, B, bases } from "@acme/kit"

export class Mixed extends mixin(A, B) {}

export class Qualified extends ns.Base {}

export class Conditional extends (cond ? A : B) {}

export class Indexed extends bases[0] {}

export class Constructed extends new Factory() {}
`,
	}, false)

	for _, name := range []string{"Mixed", "Qualified", "Conditional", "Indexed", "Constructed"} {
		f := classFact(t, ff, "src."+name)
		if f.Props[superclassProp] != nil || f.Props[superclassModuleProp] != nil {
			t.Errorf("%s claims superclass %v from %v, want silence — its base is computed",
				name, f.Props[superclassProp], f.Props[superclassModuleProp])
		}
	}
}

// TestSuperclass_ImplementsIsADifferentRelation: `implements` keeps emitting its
// relations, an interface's own `extends` is not a base class, and a class that
// does both carries both without either reading as the other.
func TestSuperclass_ImplementsIsADifferentRelation(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/serial.ts": `import { Model } from "@acme/orm"

export interface Readable extends Base {}

export class Plain implements Readable {}

export class Job extends Model implements Readable, Countable {}
`,
	}, false)

	implemented := func(f facts.Fact) []string {
		var out []string
		for _, rel := range f.Relations {
			if rel.Kind == facts.RelImplements {
				out = append(out, rel.Target)
			}
		}
		return out
	}

	plain := classFact(t, ff, "src.Plain")
	if got := implemented(plain); len(got) != 1 || got[0] != "Readable" {
		t.Errorf("Plain implements %v, want [Readable]", got)
	}
	wantSuperclass(t, plain, "", "")

	job := classFact(t, ff, "src.Job")
	got := implemented(job)
	if len(got) != 2 || got[0] != "Readable" || got[1] != "Countable" {
		t.Errorf("Job implements %v, want [Readable Countable]", got)
	}
	wantSuperclass(t, job, "Model", "@acme/orm")

	if f, ok := findFact(ff, "src.Readable"); ok && f.Props[superclassProp] != nil {
		t.Errorf("the interface's extends became a superclass: %v", f.Props)
	}
}

// TestSuperclass_NoInheritanceRelation: the props are the whole claim. An edge
// to the bare identifier would fuse every `Controller` in a repository onto one
// node that is not a class anywhere, which is the confusion the module prop
// exists to end.
func TestSuperclass_NoInheritanceRelation(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/base.ts": `export default class Base {}
`,
		"src/leaf.ts": `import Base from "./base"

export class Leaf extends Base {}
`,
	}, false)

	for _, rel := range classFact(t, ff, "src.Leaf").Relations {
		if rel.Kind == facts.RelImplements {
			t.Errorf("Leaf carries an implements edge to %q built from its extends clause", rel.Target)
		}
	}
}

// TestSuperclass_EmberClassificationUnchanged: the Ember component/service/model
// classification reads the same heritage through the same reader, so its
// framework props must survive the unification.
func TestSuperclass_EmberClassificationUnchanged(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"app/components/card.gts": `import Component from "@glimmer/component"

export default class Card extends Component {
  <template>hi</template>
}
`,
	}, false)

	f := classFact(t, ff, "app/components.Card")
	if f.Props["framework"] != "ember" {
		t.Errorf("Card props = %+v, want the ember framework classification", f.Props)
	}
	wantSuperclass(t, f, "Component", "@glimmer/component")
}
