package rubyextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An association declared in a concern belongs to every class that includes it.
// The runtime attributes it that way, so the extractor scoring against the
// runtime must too — 68 associations on the monolith are declared in eleven
// concerns and included 330 times across app/models.
//
// `include` is already an edge (7,396 of them for Ruby, carrying
// mixin_kind), so this is not new information: it is the resolver following an
// edge that was already there. That is what makes it the least speculative item
// on the epic — a declaration, not an inference.
func TestAConcernsAssociationIsAttributedToItsIncluder(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app/concerns/taggable_record.rb", `
module TaggableRecord
  extend ActiveSupport::Concern
  included do
    has_many :taggings
  end
end
`)
	write("app/models/candidate.rb", `
class Candidate < ApplicationRecord
  include TaggableRecord
end
`)
	write("app/models/tagging.rb", "class Tagging < ApplicationRecord\nend\n")
	// Association extraction is gated on the repository looking like a Rails
	// app, so the fixture has to say so.
	write("Gemfile", "gem \"rails\"\n")
	write("config/application.rb", "module App\n  class Application < Rails::Application\n  end\nend\n")

	got, err := New().Extract(context.Background(), dir, []string{
		"app/concerns/taggable_record.rb",
		"app/models/candidate.rb",
		"app/models/tagging.rb",
		"Gemfile",
		"config/application.rb",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if f.Name == "Candidate#taggings" {
			if target, _ := f.Props["target"].(string); target != "Tagging" {
				t.Fatalf("target = %q, want Tagging", target)
			}
			if via, _ := f.Props["target_source"].(string); via == "" {
				t.Fatalf("no target_source recorded: %v", f.Props)
			}
			return
		}
	}
	t.Fatalf("Candidate#taggings was not emitted; the includer got none of the concern's associations")
}
