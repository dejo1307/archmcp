package providers

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// zeitwerkScript resolves the reference provider relative to this package, so
// the test exercises the checked-in script rather than a copy.
func zeitwerkScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "providers", "zeitwerk", "enola_zeitwerk_provider.rb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reference provider missing: %v", err)
	}
	return path
}

// requireRuby skips with a named reason when the machine cannot run the
// provider — the provider degrading to a skip is the seam's contract, and the
// test honors the same shape. The zeitwerk provider needs only a plain ruby;
// no stdlib beyond json.
func requireRuby(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby is not on PATH; the zeitwerk provider cannot run here")
	}
}

func writeZeitwerkFixture(t *testing.T) string {
	t.Helper()
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
	write("app/models/user.rb", "class User\nend\n")
	write("app/models/billing/invoice_item.rb", "module Billing\n  class InvoiceItem\n  end\nend\n")
	write("app/models/concerns/archivable.rb", "module Archivable\nend\n")
	write("app/controllers/users_controller.rb", "class UsersController\nend\n")
	// The acronym edge, left fail-closed: a dash and an uppercase letter put
	// the segment outside the plain snake shape, so no constant is derived.
	write("app/models/API-helper.rb", "class APIHelper\nend\n")
	// Directories Rails never autoloads, and skip subtrees.
	write("app/javascript/controllers/dropdown_controller.rb", "class WrongPlace\nend\n")
	write("vendor/gems/vendored.rb", "class Vendored\nend\n")
	// lib/ stays out of the roots until a zeitwerk marker exists.
	write("lib/util/parser.rb", "module Util\n  class Parser\n  end\nend\n")
	return repo
}

// The golden case: nested modules, the concerns collapse, the controller
// suffix, the acronym edge skipped — and the whole exchange run through the
// seam, so acceptance is proven against the same validation production uses.
func TestZeitwerkProvider_GoldenThroughTheSeam(t *testing.T) {
	requireRuby(t)
	repo := writeZeitwerkFixture(t)
	ff, records := Run(context.Background(), []Provider{{
		Name:            "zeitwerk",
		Command:         []string{"ruby", zeitwerkScript(t)},
		ExpectedVersion: "0.1.0",
	}}, repo, nil)
	if len(records) != 1 || records[0].Skipped {
		t.Fatalf("census = %+v, want a clean run", records)
	}
	if records[0].Version != "0.1.0" {
		t.Errorf("reported version = %q", records[0].Version)
	}

	want := []struct{ name, file, constant string }{
		{"zeitwerk-map: Archivable -> app/models/concerns/archivable.rb", "app/models/concerns/archivable.rb", "Archivable"},
		{"zeitwerk-map: Billing::InvoiceItem -> app/models/billing/invoice_item.rb", "app/models/billing/invoice_item.rb", "Billing::InvoiceItem"},
		{"zeitwerk-map: User -> app/models/user.rb", "app/models/user.rb", "User"},
		{"zeitwerk-map: UsersController -> app/controllers/users_controller.rb", "app/controllers/users_controller.rb", "UsersController"},
	}
	if len(ff) != len(want) {
		t.Fatalf("facts = %d, want %d: %+v", len(ff), len(want), ff)
	}
	for i, w := range want {
		f := ff[i]
		if f.Name != w.name || f.File != w.file {
			t.Errorf("fact[%d] = %q (%s), want %q (%s)", i, f.Name, f.File, w.name, w.file)
		}
		if f.Kind != "dependency" {
			t.Errorf("fact[%d] kind = %s, want dependency", i, f.Kind)
		}
		if f.Props[PropResolutionLevel] != "convention-derived" || f.Props["mapping"] != "autoload" {
			t.Errorf("fact[%d] props = %+v, want convention-derived autoload mapping", i, f.Props)
		}
		if len(f.Relations) != 1 || f.Relations[0].Kind != "depends_on" || f.Relations[0].Target != w.constant {
			t.Errorf("fact[%d] relations = %+v, want one depends_on edge to %s", i, f.Relations, w.constant)
		}
	}
	for _, f := range ff {
		for _, banned := range []string{"API", "Vendored", "WrongPlace", "Util"} {
			if strings.Contains(f.Name, banned) {
				t.Errorf("fail-closed miss leaked into the facts: %q", f.Name)
			}
		}
	}
}

// lib/ joins the autoload roots only when a zeitwerk marker exists — here the
// Gemfile declaring the gem — and its tasks/ subtree stays out, matching the
// autoload_lib ignore defaults.
func TestZeitwerkProvider_LibNeedsAMarker(t *testing.T) {
	requireRuby(t)
	repo := writeZeitwerkFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "Gemfile"), []byte("source \"https://rubygems.org\"\ngem \"zeitwerk\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "lib", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "tasks", "cleanup.rb"), []byte("class Cleanup\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ff, records := Run(context.Background(), []Provider{{
		Name:            "zeitwerk",
		Command:         []string{"ruby", zeitwerkScript(t)},
		ExpectedVersion: "0.1.0",
	}}, repo, nil)
	if len(records) != 1 || records[0].Skipped {
		t.Fatalf("census = %+v, want a clean run", records)
	}
	found := false
	for _, f := range ff {
		if f.Name == "zeitwerk-map: Util::Parser -> lib/util/parser.rb" {
			found = true
		}
		if strings.Contains(f.Name, "Cleanup") {
			t.Errorf("lib/tasks must stay out of the roots, got %q", f.Name)
		}
	}
	if !found {
		t.Errorf("lib/util/parser.rb must map once the marker exists, facts: %+v", ff)
	}
}

func TestZeitwerkProvider_OutputIsSortedAndDeterministic(t *testing.T) {
	requireRuby(t)
	repo := writeZeitwerkFixture(t)
	script := zeitwerkScript(t)
	run := func() []byte {
		cmd := exec.Command("ruby", script, repo)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("provider run failed: %v (%s)", err, stderr.String())
		}
		if !strings.Contains(stderr.String(), "skipped 1 file(s)") {
			t.Errorf("stderr = %q, want the fail-closed summary counting the acronym-edge file", stderr.String())
		}
		return stdout.Bytes()
	}
	first, second := run(), run()
	if !bytes.Equal(first, second) {
		t.Fatalf("provider output differs across identical runs:\n%s\nvs\n%s", first, second)
	}
	lines := strings.Split(strings.TrimSpace(string(first)), "\n")
	if !sort.StringsAreSorted(lines) {
		t.Errorf("output lines are not sorted:\n%s", first)
	}
}
