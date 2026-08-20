package rubyextractor

import (
	"reflect"
	"testing"
)

// A block binding names the relation the loop iterates as the chain of names it
// was built from, arguments dropped, so a consumer can walk it back to the
// association and past the scopes and preloaders on the way. A local assigned
// from a chain is spliced in, including one that extends itself and one wrapped
// in policy_scope: the monolith's `CannedResponsesQuery#resolve` reassigns
// `canned_responses = canned_responses.includes(...)` before it maps.
func TestBlockBindings_CarryTheReceiverChainWithLocalsSpliced(t *testing.T) {
	got := propsFor(t, `
class Thing
  def call(candidate)
    Current.company.users.allowed_to_login.preload(:authorizations).each { |user| user.authorizations }
    canned_responses = @company.canned_responses
    canned_responses = canned_responses.includes(:questions)
    canned_responses.map { |canned_response| canned_response.questions }
    actions = policy_scope(candidate.actions.recent)
    actions.each { |action| action.user }
    things.each { |thing| thing.id }
  end
end
`, "block_bindings")
	want := []string{
		"action=candidate.actions.recent",
		"canned_response=@company.canned_responses.includes",
		"thing=things",
		"user=Current.company.users.allowed_to_login.preload",
	}
	if !reflect.DeepEqual(got["Thing#call"], want) {
		t.Fatalf("block_bindings = %v, want %v", got["Thing#call"], want)
	}
}

// params ride along with block bindings and nowhere else: a method with no
// bound loop states nothing new, and the fixtures the channels are held
// identical on stay identical.
func TestParams_NameTheMethodParametersOnlyWhereALoopIsBound(t *testing.T) {
	got := propsFor(t, `
class Thing
  def call(actions, limit = 10, *rest, key:, other: 1, **opts, &blk)
    actions.each { |a| a }
  end

  def plain(a, b)
    a + b
  end
end
`, "params")
	want := []string{"actions", "limit", "rest", "key", "other", "opts", "blk"}
	if !reflect.DeepEqual(got["Thing#call"], want) {
		t.Fatalf("params = %v, want %v", got["Thing#call"], want)
	}
	if _, ok := got["Thing#plain"]; ok {
		t.Fatalf("a method with no block binding must not carry params: %v", got["Thing#plain"])
	}
}

// A scope states its preloads once for every caller. The fact carries the model
// it belongs to, so a consumer joins the scope by (model, name) and never by
// name alone.
func TestScopeFacts_CarryModelAndPreloads(t *testing.T) {
	src := `
class Action < ApplicationRecord
  belongs_to :user
  scope :activity_stream_for_candidate, -> { includes(:user, :candidate).where(code: CODES).order(updated_at: :desc) }
  scope :recent, -> { order(created_at: :desc) }
end
`
	var model, preloads map[string][]string
	preloads = propsFor(t, src, "preloads")
	if !reflect.DeepEqual(preloads["scope:activity_stream_for_candidate"], []string{"candidate", "user"}) {
		t.Fatalf("scope preloads = %v", preloads["scope:activity_stream_for_candidate"])
	}
	if _, ok := preloads["scope:recent"]; ok {
		t.Fatalf("a scope with no preloader must not carry preloads")
	}
	model = map[string][]string{}
	for _, f := range extractFileAST([]byte(src), "app/models/action.rb", false, false) {
		if m, _ := f.Props["model"].(string); m != "" && f.Props["scope"] == true {
			model[f.Name] = []string{m}
		}
	}
	if !reflect.DeepEqual(model["scope:recent"], []string{"Action"}) {
		t.Fatalf("scope model = %v", model)
	}
}

func TestBatchLoader_MarksTheMethodThatHandsReadsToIt(t *testing.T) {
	src := `
class Loader
  def locations(promotion)
    BatchLoader.for(promotion.id).batch do |ids, loader|
      Promotion.where(id: ids).each { |p| loader.call(p.id, p.locations) }
    end
  end

  def plain(promotions)
    promotions.each { |p| p.locations }
  end
end
`
	marked := map[string]bool{}
	for _, f := range extractFileAST([]byte(src), "app/services/loader.rb", false, false) {
		if f.Props["batch_loader"] == true {
			marked[f.Name] = true
		}
	}
	if !marked["Loader#locations"] || marked["Loader#plain"] {
		t.Fatalf("batch_loader marks = %v", marked)
	}
}

// `Company.find_each do |company|` is the most Rails way to walk a table and
// recorded nothing: the receiver is a constant, neither a variable nor a call.
// A constant or namespaced receiver binds like any other; whether it names a
// model is the consumer's question.
func TestBlockBindings_BindAConstantReceiver(t *testing.T) {
	got := propsFor(t, `
class Thing
  def call
    Company.find_each { |company| company.users }
    Billing::Invoice.each { |invoice| invoice.lines }
    STOP_CHARS.each { |c| c }
  end
end
`, "block_bindings")
	want := []string{"c=STOP_CHARS", "company=Company", "invoice=Billing::Invoice"}
	if !reflect.DeepEqual(got["Thing#call"], want) {
		t.Fatalf("block_bindings = %v, want %v", got["Thing#call"], want)
	}
}
