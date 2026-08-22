package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/intent"
)

// A Rails layout binds the Rails recipe's required roles and names the
// optional ones it left; an arrangement missing a required directory is not
// bound and says which; one with no directory at all is not bound; and the
// written file loads.
func TestConstraintsInit_BindsOnlyWhatTheTreeHas(t *testing.T) {
	repo := t.TempDir()
	for _, d := range []string{"app/models", "app/controllers", "app/jobs", "app/mailers", "app/policies", "app/serializers", "app/components"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body, report, err := initDeclaration(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "rails-conventions    bound 7 role(s)") || !strings.Contains(report, "optional, left for the author: helpers, services") {
		t.Fatalf("report = %q", report)
	}
	if !strings.Contains(report, "cqrs                 not bound: no directory for commands, queries, read-models") || !strings.Contains(report, "vanilla-rails        not bound: no directory for") {
		t.Fatalf("an arrangement with no directory is not bound: %q", report)
	}
	if err := os.MkdirAll(filepath.Join(repo, intent.ConstraintsDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, intent.ConstraintsDirName, initFileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := intent.LoadRepoFile(repo)
	if err != nil {
		t.Fatalf("the written declaration must load: %v", err)
	}
	var found bool
	for _, c := range d.Components {
		if c.Name == "rails-conventions/models" && len(c.Match) == 1 && c.Match[0] == "app/models/**" {
			found = true
		}
	}
	if !found {
		t.Fatalf("models must be bound to app/models/**: %+v", d.Components)
	}
}
