package rubyextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func templateRefFact(result []facts.Fact) (facts.Fact, bool) {
	for _, f := range result {
		if f.Kind == facts.KindFileRef {
			return f, true
		}
	}
	return facts.Fact{}, false
}

// TestExtractTemplateRefs_ERB checks that Ruby calls in <%= %> tags are captured,
// <%# %> comments are ignored, and no symbol facts are emitted.
func TestExtractTemplateRefs_ERB(t *testing.T) {
	src := `<div>
  <%= RailsAdmin::NotificationTriggerHelper.registry_for_js.to_json %>
  <% ignored_ok = current_user.can_manage? %>
  <%# CommentHelper.should_be_ignored %>
</div>
`
	result := extractTemplateRefs([]byte(src), "app/views/rails_admin/main/quick_actions.html.erb")
	fr, ok := templateRefFact(result)
	if !ok {
		t.Fatal("missing KindFileRef fact for ERB template")
	}
	for _, want := range []string{"registry_for_js", "RailsAdmin::NotificationTriggerHelper", "can_manage?"} {
		if !hasCall(fr, want) {
			t.Errorf("ERB call should be recorded -> %s; relations = %v", want, fr.Relations)
		}
	}
	if hasCall(fr, "should_be_ignored") {
		t.Errorf("ERB comment content must be ignored; relations = %v", fr.Relations)
	}
	for _, f := range result {
		if f.Kind == facts.KindSymbol {
			t.Fatalf("template extraction must not emit symbol facts; got %v", f)
		}
	}
}

// TestExtractTemplateRefs_SlimHaml checks leading =/- markers and #{} interpolation.
func TestExtractTemplateRefs_Slim(t *testing.T) {
	src := `.container
  = SomeHelper.build_widget
  - rows = current_user.can_edit?
  p Hello #{presenter.display_name}
`
	result := extractTemplateRefs([]byte(src), "app/views/x/show.html.slim")
	fr, ok := templateRefFact(result)
	if !ok {
		t.Fatal("missing KindFileRef fact for Slim template")
	}
	for _, want := range []string{"build_widget", "can_edit?", "display_name"} {
		if !hasCall(fr, want) {
			t.Errorf("Slim call should be recorded -> %s; relations = %v", want, fr.Relations)
		}
	}
}

// TestIsTemplateFile checks the template-suffix detection.
func TestIsTemplateFile(t *testing.T) {
	for _, p := range []string{"a.html.erb", "b.js.erb", "c.html.slim", "d.haml"} {
		if !isTemplateFile(p) {
			t.Errorf("isTemplateFile(%q) = false, want true", p)
		}
	}
	if isTemplateFile("app/models/foo.rb") {
		t.Error("isTemplateFile should be false for .rb")
	}
}
