package rubyextractor

import (
	"os"
	"path/filepath"
	"testing"
)

func propsFor(t *testing.T, src, prop string) map[string][]string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app", "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := "app/services/thing.rb"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, f := range extractFileAST([]byte(src), rel, false, false) {
		if v := propList(f.Props[prop]); len(v) > 0 {
			out[f.Name] = v
		}
	}
	return out
}

// An association read on the elements of a relation that was preloaded is a
// cache hit, not a query per element. The extractor records what was
// preloaded, symbols and hash keys and values alike, and leaves the join to
// the consumer: `CannedResponsesQuery#resolve` carried `includes(:questions,
// answers: :author)` and was still reported for `q.questions` in its loop.
func TestPreloads_RecordEveryAssociationNamedInIncludesPreloadAndEagerLoad(t *testing.T) {
	got := propsFor(t, `
class Thing
  def call
    scope = CannedResponse.includes(:questions, answers: :author).preload(:tags)
    scope.eager_load(:company).each { |r| r.questions }
    Other.where(a: 1)
  end
end
`, "preloads")
	want := []string{"answers", "author", "company", "questions", "tags"}
	pre := got["Thing#call"]
	if len(pre) != len(want) {
		t.Fatalf("preloads = %v, want %v", pre, want)
	}
	for i := range want {
		if pre[i] != want[i] {
			t.Fatalf("preloads = %v, want %v", pre, want)
		}
	}
}

func TestPreloads_InterpolatedSymbolsAndOtherCallsRecordNothing(t *testing.T) {
	got := propsFor(t, `
class Thing
  def call
    CannedResponse.where(kind: :draft).includes(:"#{dyn}")
  end
end
`, "preloads")
	if pre := got["Thing#call"]; len(pre) != 0 {
		t.Fatalf("preloads = %v, want none", pre)
	}
}

// A local typed by `new` holds a record that has never been saved: reading
// its associations builds in memory and queries nothing. `Company.new`
// followed by `company.pages.build` in a loop is a mock (BlockLayoutsController#mock_company),
// not an N+1, and the consumer needs to know how the local was typed.
func TestUnpersistedLocals_NameTheLocalsTypedByNew(t *testing.T) {
	got := propsFor(t, `
class Thing
  def call
    company = Company.new(name: "x")
    real = Company.find(1)
    @draft = Draft.new
    [company, real].each { |c| c.pages }
  end
end
`, "unpersisted_locals")
	want := []string{"@draft", "company"}
	un := got["Thing#call"]
	if len(un) != 2 || un[0] != want[0] || un[1] != want[1] {
		t.Fatalf("unpersisted_locals = %v, want %v", un, want)
	}
	types := localTypesFor(t, `
class Thing
  def call
    company = Company.new(name: "x")
    real = Company.find(1)
  end
end
`)["Thing#call"]
	if len(types) != 2 {
		t.Fatalf("local_types unchanged in shape, got %v", types)
	}
}
