package rubyextractor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// TestTurboFrames_DeclaredAndFailClosed: literal frame ids from the tag helper
// and the targeting attribute become facts, sorted by id; everything whose
// rendered identity is not knowable statically — dom_id calls, interpolation,
// ERB inside the attribute, the reserved _top target — emits nothing.
func TestTurboFrames_DeclaredAndFailClosed(t *testing.T) {
	src := []byte(`<%= turbo_frame_tag :post_1 do %>
  <%= turbo_frame_tag "comments" %>
  <a data-turbo-frame="results">search</a>
  <%= turbo_frame_tag dom_id(@post) %>
  <%= turbo_frame_tag "post_#{@post.id}" %>
  <a data-turbo-frame="<%= frame %>">no</a>
  <a data-turbo-frame="_top">whole page</a>
<% end %>
`)
	ff := extractTurboFrames("app/views/posts/show.html.erb", src)
	if len(ff) != 3 {
		t.Fatalf("expected 3 frame facts, got %d: %+v", len(ff), ff)
	}
	wantNames := []string{
		"turbo-frame: app/views/posts/show.html.erb -> comments",
		"turbo-frame: app/views/posts/show.html.erb -> post_1",
		"turbo-frame: app/views/posts/show.html.erb -> results",
	}
	for i, want := range wantNames {
		if ff[i].Name != want {
			t.Errorf("fact %d name = %q, want %q (sorted by id)", i, ff[i].Name, want)
		}
		if ff[i].Kind != facts.KindDependency {
			t.Errorf("fact %d kind = %q, want dependency", i, ff[i].Kind)
		}
		if ff[i].Props["resolution_level"] != "markup-declared" || ff[i].Props["framework"] != "turbo" {
			t.Errorf("fact %d props = %+v, want markup-declared turbo", i, ff[i].Props)
		}
		if len(ff[i].Relations) != 0 {
			t.Errorf("fact %d relations = %+v, want none: a frame id is an identity, not a resolved target", i, ff[i].Relations)
		}
	}
	if ff[2].Props["binding"] != "data-turbo-frame" {
		t.Errorf("results binding = %v, want the declaring attribute", ff[2].Props["binding"])
	}
	if ff[1].Props["binding"] != "turbo_frame_tag" {
		t.Errorf("post_1 binding = %v, want the declaring helper", ff[1].Props["binding"])
	}
}

// TestTurboFrames_ElementForm: a hand-written <turbo-frame id="…"> element
// declares its frame id exactly as the tag helper does — it is the helper's
// rendered output — and an id carrying ERB still emits nothing.
func TestTurboFrames_ElementForm(t *testing.T) {
	src := []byte(`<turbo-frame id="composer-frame">
  <span>This room was deleted.</span>
</turbo-frame>
<turbo-frame id="<%= dom_id(@room) %>">rendered identity unknowable</turbo-frame>
<turbo-frame target="_top">no id declared</turbo-frame>
`)
	ff := extractTurboFrames("app/views/messages/room_not_found.html.erb", src)
	if len(ff) != 1 {
		t.Fatalf("expected 1 frame fact, got %d: %+v", len(ff), ff)
	}
	if want := "turbo-frame: app/views/messages/room_not_found.html.erb -> composer-frame"; ff[0].Name != want {
		t.Errorf("fact name = %q, want %q", ff[0].Name, want)
	}
	if ff[0].Props["binding"] != "<turbo-frame>" {
		t.Errorf("binding = %v, want the declaring element", ff[0].Props["binding"])
	}
}

// TestBroadcastsTo_LiteralStreamOnly: a symbol or plain-string stream becomes
// one broadcast fact per (model, stream); the common lambda form computes its
// stream at runtime per record, so it emits nothing — a counted absence,
// never a guess.
func TestBroadcastsTo_LiteralStreamOnly(t *testing.T) {
	repo := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app/models/post.rb", "class Post < ApplicationRecord\n  broadcasts_to :posts, inserts_by: :prepend\nend\n")
	write("app/models/board.rb", "class Board < ApplicationRecord\n  broadcasts_to \"boards\"\nend\n")
	write("app/models/comment.rb", "class Comment < ApplicationRecord\n  broadcasts_to ->(comment) { [comment.post, :comments] }\nend\n")
	write("app/models/note.rb", "class Note < ApplicationRecord\n  broadcasts_to \"note_#{Rails.env}\"\nend\n")

	ff := extractBroadcasts(repo, []string{
		"app/models/post.rb", "app/models/board.rb", "app/models/comment.rb", "app/models/note.rb",
	})
	if len(ff) != 2 {
		t.Fatalf("expected 2 broadcast facts, got %d: %+v", len(ff), ff)
	}
	if ff[0].Name != "broadcast: Board -> boards" || ff[1].Name != "broadcast: Post -> posts" {
		t.Errorf("facts = %q, %q, want the sorted literal pairs", ff[0].Name, ff[1].Name)
	}
	for i, f := range ff {
		if f.Kind != facts.KindDependency || f.Props["macro"] != "broadcasts_to" || f.Props["resolution_level"] != "literal-declared" {
			t.Errorf("fact %d = %+v, want a literal-declared broadcasts_to dependency", i, f)
		}
	}
	if ff[1].File != "app/models/post.rb" || ff[1].Line != 2 {
		t.Errorf("post fact location = %s:%d, want app/models/post.rb:2", ff[1].File, ff[1].Line)
	}
}
