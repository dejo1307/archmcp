package tsextractor

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// TestStimulusStaticFields_ClassifiedOnControllerSymbols: the static
// targets/values fields of a conventionally-placed controller carry the
// stimulus classification props; the controller's methods do not, and the
// same shape outside a controller file stays untouched — fail closed.
func TestStimulusStaticFields_ClassifiedOnControllerSymbols(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"app/javascript/controllers/dropdown_controller.ts": `import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = ["menu", "button"]
  static values = { open: Boolean }

  toggle() {
    this.menuTarget.classList.toggle("open")
  }
}
`,
		"src/table.ts": `export class Table {
  static targets = ["nothing to do with stimulus"]
}
`,
	}, false)

	byName := map[string]facts.Fact{}
	for _, f := range ff {
		if f.Kind == facts.KindSymbol {
			byName[f.Name] = f
		}
	}

	classified := 0
	for name, f := range byName {
		tagged := f.Props["stimulus_static"] != nil
		switch {
		case strings.HasPrefix(name, "app/javascript/controllers.") && strings.HasSuffix(name, ".targets"):
			if !tagged || f.Props["framework"] != "stimulus" || f.Props["stimulus_static"] != "targets" {
				t.Errorf("controller targets field %q props = %+v, want the stimulus classification", name, f.Props)
			}
			classified++
		case strings.HasPrefix(name, "app/javascript/controllers.") && strings.HasSuffix(name, ".values"):
			if !tagged || f.Props["stimulus_static"] != "values" {
				t.Errorf("controller values field %q props = %+v, want the stimulus classification", name, f.Props)
			}
			classified++
		case tagged:
			t.Errorf("symbol %q must not carry stimulus props: %+v", name, f.Props)
		}
	}
	if classified != 2 {
		t.Fatalf("classified %d static fields, want targets and values; symbols: %v", classified, names(byName))
	}
}

func names(byName map[string]facts.Fact) []string {
	out := make([]string, 0, len(byName))
	for name := range byName {
		out = append(out, name)
	}
	return out
}
