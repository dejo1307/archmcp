package rubyextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func bindingsFor(t *testing.T, src string) map[string][]string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "app", "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := "app/models/thing.rb"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := New().Extract(context.Background(), dir, []string{rel})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, f := range facts {
		if b := propList(f.Props["block_bindings"]); len(b) > 0 {
			out[f.Name] = b
		}
	}
	return out
}

// The receiver a block parameter is bound to is the one piece of type
// information Ruby hands over for free, and it is exactly the N+1 shape:
// `form_questions.each { |q| q.form_answers }` is a query per iteration only
// because `q` is a FormQuestion. Without the binding, `q.form_answers` and
// `client.post` are the same string — which is why the association-read
// detector measured to zero true findings.
func TestABlockParameterIsBoundToItsCollection(t *testing.T) {
	got := bindingsFor(t, `
class Thing
  def call
    form_questions.each do |question|
      question.form_answers
    end
  end
end
`)
	binds := got["Thing#call"]
	if len(binds) != 1 || binds[0] != "question=form_questions" {
		t.Fatalf("want question=form_questions, got %v", binds)
	}
}

func TestBraceBlocksBindToo(t *testing.T) {
	got := bindingsFor(t, `
class Thing
  def call
    candidates.map { |c| c.name }
  end
end
`)
	if binds := got["Thing#call"]; len(binds) != 1 || binds[0] != "c=candidates" {
		t.Fatalf("want c=candidates, got %v", binds)
	}
}

// A block over a literal or a method with no collection meaning binds nothing
// worth recording — the point is the collection's identity, not the variable.
func TestABlockWithoutAReceiverBindsNothing(t *testing.T) {
	got := bindingsFor(t, `
class Thing
  def call
    [1, 2].each { |n| n.to_s }
  end
end
`)
	if binds, ok := got["Thing#call"]; ok {
		t.Fatalf("a literal collection produced bindings: %v", binds)
	}
}
