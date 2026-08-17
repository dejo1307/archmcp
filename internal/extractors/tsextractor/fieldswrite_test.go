package tsextractor

import "testing"

// The convention is that data flows down and actions flow up, so a component
// writing through its own arguments is the violation. Recording the outermost
// property after `this` is what makes it selectable.
func TestExtract_FieldsWrittenRecordsTheRootOfAThisAssignment(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/form.ts": `export class Form {
  rename(value: string) {
    this.args.user.name = value;
  }

  select(id: string) {
    this.selected = id;
  }

  notify(value: string) {
    this.args.onRename(value);
  }

  local() {
    const other = { name: '' };
    other.name = 'x';
  }
}`,
	}, false)

	for _, c := range []struct {
		symbol string
		want   string
	}{
		{"src.Form.rename", "args"},
		{"src.Form.select", "selected"},
	} {
		f, ok := findFact(ff, c.symbol)
		if !ok {
			t.Fatalf("no fact for %s", c.symbol)
		}
		written, _ := f.Props["fields_written"].([]string)
		if len(written) != 1 || written[0] != c.want {
			t.Errorf("%s fields_written = %v, want [%s]", c.symbol, f.Props["fields_written"], c.want)
		}
	}

	// Calling through an argument is the pattern the convention asks FOR, and
	// an assignment to something that is not `this` is not a claim about the
	// member's own state. Recording either would make the prop a list of
	// everything the body touches.
	for _, quiet := range []string{"src.Form.notify", "src.Form.local"} {
		f, ok := findFact(ff, quiet)
		if !ok {
			t.Fatalf("no fact for %s", quiet)
		}
		if _, present := f.Props["fields_written"]; present {
			t.Errorf("%s carries fields_written = %v, want none", quiet, f.Props["fields_written"])
		}
	}
}
