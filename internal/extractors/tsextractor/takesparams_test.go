package tsextractor

import "testing"

// A modifier is handed the element it is attached to, so whether a member
// declares a parameter at all is the difference between something that
// modifies its element and a side effect fired by render. The prop is emitted
// on every member rather than only where it is true, because a rule reads it
// as a value to match and an absent prop selects nobody.
func TestExtract_TakesParametersRecordsWhetherAMemberDeclaresOne(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/panel.ts": `export class Panel {
  focus(element: HTMLElement) {
    element.focus();
  }

  start() {
    this.load();
  }

  reset(...rest: string[]) {
    void rest;
  }

  get title() {
    return 'x';
  }
}`,
	}, false)

	for _, c := range []struct {
		symbol string
		want   string
	}{
		{"src.Panel.focus", "yes"},
		{"src.Panel.start", "no"},
		{"src.Panel.reset", "yes"},
		{"src.Panel.title", "no"},
	} {
		f, ok := findFact(ff, c.symbol)
		if !ok {
			t.Fatalf("no fact for %s", c.symbol)
		}
		got, _ := f.Props["takes_parameters"].(string)
		if got != c.want {
			t.Errorf("%s takes_parameters = %v, want [%s]", c.symbol, f.Props["takes_parameters"], c.want)
		}
	}
}

// The dominant modifier form is a module-level default export, not a class
// field, and the prop was emitted only on class members — so every such
// modifier carried no answer at all while a rule demanding "yes" read the
// silence as a breach. Measured on a real Ember application, all eight members
// the rule named were module-level modifiers that do take their element. The
// parameters live on the callback handed to `modifier(...)`, which is the
// member's own signature here in every sense that matters to the convention.
func TestExtract_TakesParametersReachesModuleLevelFunctionSymbols(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"app/modifiers/hover-state.ts": `import { modifier } from 'ember-modifier';
export default modifier((element, [action]: [(_: boolean) => void]) => {
  element.addEventListener('mouseenter', () => action(true));
});`,
		"app/modifiers/set-el.ts": `import { modifier } from 'ember-modifier';
export default modifier(function setEl<K extends string>(el: HTMLElement, [owner]: [K]) {
  void el;
  void owner;
});`,
		"app/modifiers/style-prop.ts": `import { modifier } from 'ember-modifier';
export default modifier<Sig>((element: HTMLElement, [name]: [string]) => {
  element.setAttribute(name, '');
});`,
		"app/modifiers/ping.ts": `import { modifier } from 'ember-modifier';
export default modifier(() => {
  track('rendered');
});`,
		"app/utils/plain.ts": `export function plain(a: string) {
  return a;
}

export function bare() {
  return 1;
}`,
	}, false)

	for _, c := range []struct {
		symbol string
		want   string
	}{
		{"app/modifiers.HoverState", "yes"},
		{"app/modifiers.SetEl", "yes"},
		{"app/modifiers.StyleProp", "yes"},
		// The one the convention is actually about: handed an element, ignores
		// it, and is therefore a side effect fired by render.
		{"app/modifiers.Ping", "no"},
		{"app/utils.plain", "yes"},
		{"app/utils.bare", "no"},
	} {
		f, ok := findFact(ff, c.symbol)
		if !ok {
			t.Fatalf("no fact for %s", c.symbol)
		}
		got, _ := f.Props["takes_parameters"].(string)
		if got != c.want {
			t.Errorf("%s takes_parameters = %q, want %q", c.symbol, got, c.want)
		}
	}
}
