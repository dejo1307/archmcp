package intent

import (
	"strings"
	"testing"
)

// One file per convention means two conventions may speak about the same part
// of the application, so a part declared identically in both files is the same
// part said twice rather than a collision. A name reused for a different
// selector stays the error the screen exists for.
func TestComponentCollision_IdenticalRedeclarationIsTheSamePart(t *testing.T) {
	jobs := ConstraintComponent{Name: "jobs", Match: []string{"app/jobs/**"}, SourceFile: "enola/constraints/a.rb"}
	same := ConstraintComponent{Name: "jobs", Match: []string{"app/jobs/**"}, SourceFile: "enola/constraints/b.rb"}
	other := ConstraintComponent{Name: "jobs", Match: []string{"app/workers/**"}, SourceFile: "enola/constraints/c.rb"}
	controllers := ConstraintComponent{Name: "controllers", Match: []string{"app/controllers/**"}, SourceFile: "enola/constraints/a.rb"}
	rule := ConstraintRule{ID: "jobs-do-not-call-controllers", Forbid: "jobs", To: "controllers", Via: "calls",
		Because: "a job runs without a request", SourceFile: "enola/constraints/a.rb"}

	agreeing := &Declaration{Components: []ConstraintComponent{jobs, controllers, same}, Rules: []ConstraintRule{rule}}
	for _, p := range agreeing.Problems() {
		if strings.Contains(p, "already declared") {
			t.Fatalf("an identical redeclaration must not collide: %s", p)
		}
	}
	if err := agreeing.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want a clean declaration", err)
	}

	conflicting := &Declaration{Components: []ConstraintComponent{jobs, controllers, other}, Rules: []ConstraintRule{rule}}
	found := false
	for _, p := range conflicting.Problems() {
		if strings.Contains(p, "already declared") && strings.Contains(p, "different selector") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a name reused for another selector must still collide: %v", conflicting.Problems())
	}
}
