package rubyextractor

import (
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func callTargets(t *testing.T, src string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, f := range extractFileAST([]byte(src), "app/models/thing.rb", true, false) {
		for _, r := range f.Relations {
			if r.Kind == facts.RelCalls {
				out[f.Name] = append(out[f.Name], r.Target)
			}
		}
		sort.Strings(out[f.Name])
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// A method used only through symbol-to-proc or named by a class-body DSL
// looked unreferenced, and a dead-method reading would have listed it. Each
// of these is a call the graph now carries.
func TestMethodReferences_SymbolToProcAndNamingDSLAreCalls(t *testing.T) {
	got := callTargets(t, `
class Thing < ApplicationRecord
  before_save :stamp, if: :stampable?
  validates :name, presence: true, unless: :anonymous?
  rescue_from ActiveRecord::RecordNotFound, with: :render_not_found
  helper_method :current_scope
  field :profile_url, String, null: true
  alias_method :old_name, :new_name

  def purge
    pages.each(&:destroy_with_publication!)
    names.map(&:downcase)
    DestroyJob.perform_async(id, :requisitions_done)
  end
end
`)
	named := map[string][]string{}
	for _, f := range extractFileAST([]byte("class Thing\n  def purge\n    DestroyJob.perform_async(id, :requisitions_done, by: :admin)\n    batch.on(:success, \"CompanyDeletion::DestroyCompanyJob#users_done\", \"company_id\" => 1)\n    log(\"Starting Thing.purge now\")\n  end\nend\n"), "app/models/thing.rb", true, false) {
		for _, r := range f.Relations {
			if r.Kind == facts.RelNames {
				named[f.Name] = append(named[f.Name], r.Target)
			}
		}
	}
	if !has(named["Thing#purge"], "requisitions_done") || !has(named["Thing#purge"], "admin") {
		t.Fatalf("symbol argument naming: %v (want requisitions_done and the keyword value admin)", named["Thing#purge"])
	}
	for _, f := range extractFileAST([]byte("class Thing\n  ATTRS = %i[wallet_id cookie_setting_id]\n  def purge(parser)\n    parser.usernames\n    parser.mentioned_ids\n  end\nend\n"), "app/models/thing.rb", true, false) {
		for _, r := range f.Relations {
			if r.Kind == facts.RelNames {
				named[f.Name] = append(named[f.Name], r.Target)
			}
		}
	}
	if !has(named["Thing"], "wallet_id") || !has(named["Thing"], "cookie_setting_id") {
		t.Fatalf("%%i symbol array naming: %v", named["Thing"])
	}
	if !has(named["Thing#purge"], "usernames") {
		t.Fatalf("a single-word read on a variable receiver must be a name: %v", named["Thing#purge"])
	}
	if !has(named["Thing#purge"], "users_done") || has(named["Thing#purge"], "purge now") {
		t.Fatalf("string naming: %v (want users_done from \"Class#method\", not prose)", named["Thing#purge"])
	}
	for _, want := range []string{"stamp", "stampable?", "anonymous?", "render_not_found", "current_scope", "profile_url", "new_name"} {
		if !has(got["Thing"], want) {
			t.Fatalf("class-body DSL did not record a call to %q: %v", want, got["Thing"])
		}
	}
	if has(got["Thing"], "old_name") {
		t.Fatalf("alias_method's new name is a definition, not a call: %v", got["Thing"])
	}
	for _, want := range []string{"destroy_with_publication!", "downcase"} {
		if !has(got["Thing#purge"], want) {
			t.Fatalf("&:%s was not recorded as a call: %v", want, got["Thing#purge"])
		}
	}
}
