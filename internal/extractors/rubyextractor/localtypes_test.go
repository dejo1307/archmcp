package rubyextractor

import (
	"os"
	"path/filepath"
	"testing"
)

func localTypesFor(t *testing.T, src string) map[string][]string {
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
		if v := propList(f.Props["local_types"]); len(v) > 0 {
			out[f.Name] = v
		}
	}
	return out
}

// A variable assigned from a model class is typed for the rest of the method,
// and that is the receiver information the graph has never had: 1,062 such
// assignments on the monolith, and 1,238 association reads on the variables
// they type. Without it `@meeting.candidates` and `client.post` are the same
// shape — which is what made the association N+1 measure to zero.
func TestALocalAssignedFromAConstantIsTyped(t *testing.T) {
	got := localTypesFor(t, `
class Thing
  def call
    meeting = Meeting.find(id)
    meeting.candidates
  end
end
`)
	binds := got["Thing#call"]
	if len(binds) != 1 || binds[0] != "meeting=Meeting" {
		t.Fatalf("want meeting=Meeting, got %v", binds)
	}
}

func TestAnInstanceVariableAssignedFromAConstantIsTyped(t *testing.T) {
	got := localTypesFor(t, `
class Thing
  def call
    @company = Company.new
  end
end
`)
	if binds := got["Thing#call"]; len(binds) != 1 || binds[0] != "@company=Company" {
		t.Fatalf("want @company=Company, got %v", binds)
	}
}

// Only a constant receiver types anything. `x = thing.other` says nothing about
// x, and recording a guess there is the name-coincidence failure that produced
// seven candidates and zero true findings.
func TestAssignmentFromANonConstantTypesNothing(t *testing.T) {
	got := localTypesFor(t, `
class Thing
  def call
    x = helper.build
  end
end
`)
	if binds, ok := got["Thing#call"]; ok {
		t.Fatalf("a non-constant assignment typed something: %v", binds)
	}
}
