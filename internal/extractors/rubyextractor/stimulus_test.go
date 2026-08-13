package rubyextractor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// writeStimulusFixture lays out a repo with one resolvable controller
// (dropdown) and none for the other identifiers a view may declare.
func writeStimulusFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	dir := filepath.Join(repo, "app", "javascript", "controllers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dropdown_controller.js"), []byte("export default class {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestStimulusBindings_DeclaredAndResolved: a data-controller identifier whose
// conventional controller file exists links to it; one whose file does not
// exist stays a name-only fact — declared, never guessed into an edge — and a
// data-action names its controller the same way. Output is sorted by
// identifier regardless of attribute order in the markup.
func TestStimulusBindings_DeclaredAndResolved(t *testing.T) {
	repo := writeStimulusFixture(t)
	src := []byte(`<div data-controller="modal dropdown">
  <button data-action="click->dropdown#toggle">Open</button>
  <span data-action="archive#confirm">Archive</span>
</div>
`)
	ff := extractStimulusBindings(repo, "app/views/things/show.html.erb", src, nil)
	if len(ff) != 3 {
		t.Fatalf("expected 3 binding facts, got %d: %+v", len(ff), ff)
	}
	wantNames := []string{
		"stimulus-binding: app/views/things/show.html.erb -> archive",
		"stimulus-binding: app/views/things/show.html.erb -> dropdown",
		"stimulus-binding: app/views/things/show.html.erb -> modal",
	}
	for i, want := range wantNames {
		if ff[i].Name != want {
			t.Errorf("fact %d name = %q, want %q (sorted by identifier)", i, ff[i].Name, want)
		}
		if ff[i].Kind != facts.KindDependency {
			t.Errorf("fact %d kind = %q, want dependency", i, ff[i].Kind)
		}
		if ff[i].Props["resolution_level"] != "markup-declared" {
			t.Errorf("fact %d resolution_level = %v, want markup-declared", i, ff[i].Props["resolution_level"])
		}
	}

	dropdown := ff[1]
	if len(dropdown.Relations) != 1 || dropdown.Relations[0].Target != "app/javascript/controllers/dropdown_controller.js" {
		t.Errorf("dropdown should link to its existing controller file, got %+v", dropdown.Relations)
	}
	if dropdown.Relations[0].Kind != facts.RelDependsOn {
		t.Errorf("binding edge kind = %q, want depends_on", dropdown.Relations[0].Kind)
	}
	if dropdown.Props["binding"] != "data-action data-controller" {
		t.Errorf("dropdown binding prop = %v, want both declaring attributes, sorted", dropdown.Props["binding"])
	}

	for _, unresolved := range []facts.Fact{ff[0], ff[2]} {
		if len(unresolved.Relations) != 0 {
			t.Errorf("%s has no controller file and must stay name-only, got %+v", unresolved.Name, unresolved.Relations)
		}
	}
	if ff[2].Props["binding"] != "data-controller" || ff[0].Props["binding"] != "data-action" {
		t.Errorf("binding props should name only the declaring attribute: modal=%v archive=%v", ff[2].Props["binding"], ff[0].Props["binding"])
	}
}

// TestStimulusBindings_FailClosed: interpolated values, non-ERB templates and
// namespaced identifiers behave exactly as declared — nothing is guessed.
func TestStimulusBindings_FailClosed(t *testing.T) {
	repo := writeStimulusFixture(t)
	nested := filepath.Join(repo, "app", "javascript", "controllers", "users")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "date_picker_controller.ts"), []byte("export default class {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	interpolated := []byte(`<div data-controller="<%= helper %>"></div>`)
	if got := extractStimulusBindings(repo, "app/views/x.html.erb", interpolated, nil); len(got) != 0 {
		t.Errorf("an interpolated identifier is not a Stimulus token and must declare nothing, got %+v", got)
	}

	slim := []byte(`<div data-controller="dropdown"></div>`)
	if got := extractStimulusBindings(repo, "app/views/x.html.slim", slim, nil); len(got) != 0 {
		t.Errorf("only .html.erb is in scope for this pass, got %+v", got)
	}

	namespaced := []byte(`<div data-controller="users--date-picker"></div>`)
	got := extractStimulusBindings(repo, "app/views/y.html.erb", namespaced, nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 binding fact, got %d", len(got))
	}
	if len(got[0].Relations) != 1 || got[0].Relations[0].Target != "app/javascript/controllers/users/date_picker_controller.ts" {
		t.Errorf("namespaced identifier should map -- to a directory and dashes to underscores, got %+v", got[0].Relations)
	}
}

// TestStimulusBindings_HandlersAndRelocatedRoot: the method after the `#` is
// the point of a data-action and is carried on the binding, action options are
// not part of it, and a controller outside the conventional root still grounds
// when exactly one file in the tree carries its path. The defect this pins: the
// method was cut off and thrown away, so a handler invoked only from markup had
// no inbound edge at all, and every binding in an app keeping its controllers
// under app/components was name-only.
func TestStimulusBindings_HandlersAndRelocatedRoot(t *testing.T) {
	repo := writeStimulusFixture(t)
	controllers := newStimulusControllerIndex([]string{
		"app/javascript/controllers/dropdown_controller.js",
		"app/components/connect/carousel_controller.js",
		"app/components/forms/question_controller.js",
	})

	src := []byte(`<div data-controller="dropdown connect--carousel">
  <button data-action="click->dropdown#toggle keydown.esc->dropdown#close">Open</button>
  <form data-action="submit->connect--carousel#next:prevent"></form>
</div>
`)
	ff := extractStimulusBindings(repo, "app/views/things/show.html.erb", src, controllers)
	if len(ff) != 2 {
		t.Fatalf("expected 2 binding facts, got %d: %+v", len(ff), ff)
	}
	carousel, dropdown := ff[0], ff[1]
	if carousel.Props["stimulus_handlers"] != "next" {
		t.Errorf("carousel handlers = %v, want next — an action option is not part of the method name", carousel.Props["stimulus_handlers"])
	}
	if len(carousel.Relations) != 1 || carousel.Relations[0].Target != "app/components/connect/carousel_controller.js" {
		t.Errorf("a controller outside the conventional root should still ground, got %+v", carousel.Relations)
	}
	if dropdown.Props["stimulus_handlers"] != "close toggle" {
		t.Errorf("dropdown handlers = %v, want the sorted set of both methods", dropdown.Props["stimulus_handlers"])
	}
	if len(dropdown.Relations) != 1 || dropdown.Relations[0].Target != "app/javascript/controllers/dropdown_controller.js" {
		t.Errorf("the conventional root wins outright, got %+v", dropdown.Relations)
	}
}

// TestStimulusBindings_HandlerFailsClosed: a descriptor with no method names no
// handler, and an identifier two files in the tree could answer to grounds on
// neither.
func TestStimulusBindings_HandlerFailsClosed(t *testing.T) {
	repo := t.TempDir()
	controllers := newStimulusControllerIndex([]string{
		"app/components/tasks/modal_controller.js",
		"app/javascript/legacy/modal_controller.js",
	})

	ff := extractStimulusBindings(repo, "app/views/x.html.erb", []byte(`<div data-controller="modal"></div>`), controllers)
	if len(ff) != 1 {
		t.Fatalf("expected 1 binding fact, got %d", len(ff))
	}
	if len(ff[0].Relations) != 0 {
		t.Errorf("two files answer to the same identifier — that is ambiguous and grounds nothing, got %+v", ff[0].Relations)
	}
	if _, carried := ff[0].Props["stimulus_handlers"]; carried {
		t.Error("a data-controller declares no handler")
	}

	noMethod := extractStimulusBindings(repo, "app/views/y.html.erb", []byte(`<div data-action="click->modal"></div>`), controllers)
	if len(noMethod) != 0 {
		t.Errorf("a descriptor naming no method declares nothing, got %+v", noMethod)
	}
}
