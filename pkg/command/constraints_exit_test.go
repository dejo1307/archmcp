package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every constraints subcommand is a command in its own right: once it has
// done its work the process exits with that subcommand's code and never
// reaches the path-or-unknown-command handling that follows dispatch. Before
// this test, `constraints init` wrote its declaration and then exited 1 with
// "unknown command", which read as a failure to the person who had just
// watched the file land.
func TestConstraintsSubcommandsExitAsThemselves(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "enola")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/enola")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	repo := t.TempDir()
	for _, d := range []string{"app/models", "app/controllers"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "app/models/user.rb"), []byte("class User\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, out := func() (int, string) {
		cmd := exec.Command(bin, "--generate", ".")
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			return 1, string(out)
		}
		return 0, string(out)
	}(); code != 0 {
		t.Fatalf("generate: %s", out)
	}

	run := func(args ...string) (int, string) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return code, string(out)
	}

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"init", []string{"constraints", "init", "."}, 0},
		{"lint", []string{"constraints", "lint", "."}, 0},
		{"mine", []string{"constraints", "mine", "."}, 0},
		{"explain", []string{"constraints", "explain", "app/models/user.rb", "."}, 0},
	}
	for _, tc := range cases {
		code, out := run(tc.args...)
		if strings.Contains(out, "unknown command") {
			t.Errorf("%s: fell through to the unknown-command line:\n%s", tc.name, out)
		}
		if code != tc.want {
			t.Errorf("%s: exit %d, want %d\n%s", tc.name, code, tc.want, out)
		}
	}
}
