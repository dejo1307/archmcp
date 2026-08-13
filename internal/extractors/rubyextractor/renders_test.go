package rubyextractor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func writeRenderFixture(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("<p>partial</p>\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRenderTargets_DeclaredAndResolved: every literal render form becomes one
// named fact, sorted by target; a target whose partial exists under Rails'
// lookup convention links to it, and one whose partial does not stays
// name-only — a declared render the graph could not ground.
func TestRenderTargets_DeclaredAndResolved(t *testing.T) {
	repo := t.TempDir()
	writeRenderFixture(t, repo,
		"app/views/accounts/_help_contact.html.erb",
		"app/views/users/_form.html.erb",
	)
	src := []byte(`<%= render "accounts/help_contact" %>
<%= render 'form' %>
<%= render(partial: "accounts/help_contact") %>
<%= render "users/missing_partial" %>
`)
	ff := extractRenderTargets(repo, "app/views/users/new.html.erb", src)
	if len(ff) != 3 {
		t.Fatalf("expected 3 render facts, got %d: %+v", len(ff), ff)
	}

	wantNames := []string{
		"render: app/views/users/new.html.erb -> accounts/help_contact",
		"render: app/views/users/new.html.erb -> form",
		"render: app/views/users/new.html.erb -> users/missing_partial",
	}
	wantTargets := []string{
		"app/views/accounts/_help_contact.html.erb",
		"app/views/users/_form.html.erb",
		"",
	}
	for i, want := range wantNames {
		if ff[i].Name != want {
			t.Errorf("fact %d name = %q, want %q (sorted by target)", i, ff[i].Name, want)
		}
		if ff[i].Kind != facts.KindDependency {
			t.Errorf("fact %d kind = %q, want dependency", i, ff[i].Kind)
		}
		if ff[i].Props["resolution_level"] != "literal-declared" || ff[i].Props["framework"] != "rails" {
			t.Errorf("fact %d props = %+v, want literal-declared rails", i, ff[i].Props)
		}
		if wantTargets[i] == "" {
			if len(ff[i].Relations) != 0 {
				t.Errorf("fact %d relations = %+v, want none: the partial does not exist", i, ff[i].Relations)
			}
			continue
		}
		if len(ff[i].Relations) != 1 || ff[i].Relations[0].Target != wantTargets[i] {
			t.Errorf("fact %d relations = %+v, want depends_on %s", i, ff[i].Relations, wantTargets[i])
		}
	}
}

// TestRenderTargets_FailClosed: everything short of a plain literal — the
// polymorphic object form, interpolation, a variable, a helper call — declares
// nothing, and a directory-qualified target rendered from outside any views
// tree has no root to resolve against, so it stays name-only.
func TestRenderTargets_FailClosed(t *testing.T) {
	repo := t.TempDir()
	src := []byte(`<%= render @post %>
<%= render "posts/#{@post.id}" %>
<%= render partial_name %>
<%= render partial: helper_call("x") %>
<%= render "" %>
`)
	if ff := extractRenderTargets(repo, "app/views/posts/show.html.erb", src); len(ff) != 0 {
		t.Fatalf("expected no facts from non-literal renders, got %+v", ff)
	}

	writeRenderFixture(t, repo, "lib/templates/_row.html.erb")
	outside := extractRenderTargets(repo, "lib/templates/table.html.erb", []byte(`<%= render "shared/row" %>`))
	if len(outside) != 1 || len(outside[0].Relations) != 0 {
		t.Fatalf("a views-rooted target outside a views tree must stay name-only, got %+v", outside)
	}

	beside := extractRenderTargets(repo, "lib/templates/table.html.erb", []byte(`<%= render "row" %>`))
	if len(beside) != 1 || len(beside[0].Relations) != 1 || beside[0].Relations[0].Target != "lib/templates/_row.html.erb" {
		t.Fatalf("a bare name resolves beside the calling template in any tree, got %+v", beside)
	}
}
