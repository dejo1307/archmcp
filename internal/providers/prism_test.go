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

// prismScript resolves the reference provider relative to this package, so the
// test exercises the checked-in script rather than a copy.
func prismScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "examples", "providers", "ruby", "prism", "enola_prism_provider.rb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reference provider missing: %v", err)
	}
	return path
}

// requirePrism skips with a named reason when the machine cannot run the
// provider — the provider degrading to a skip is the seam's contract, and the
// test honors the same shape.
func requirePrism(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ruby"); err != nil {
		t.Skip("ruby is not on PATH; the prism provider cannot run here")
	}
	if err := exec.Command("ruby", "-e", `require "prism"`).Run(); err != nil {
		t.Skip("this ruby cannot load the prism stdlib (needs 3.3+); the prism provider cannot run here")
	}
}

func writePrismFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	billing := "class Billing\n" +
		"  def charge(invoice)\n" +
		"    Ledger.record(invoice)\n" +
		"    validate\n" +
		"    self.notify\n" +
		"    invoice.settle\n" +
		"  end\n" +
		"end\n"
	if err := os.MkdirAll(filepath.Join(repo, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "billing.rb"), []byte(billing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "vendor", "gems"), 0o755); err != nil {
		t.Fatal(err)
	}
	vendored := "class Vendored\n  def leak\n    Secret.read\n  end\nend\n"
	if err := os.WriteFile(filepath.Join(repo, "vendor", "gems", "v.rb"), []byte(vendored), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

// The golden case: one fixture file, every receiver-typing rule exercised
// once, and the whole exchange run through the seam so acceptance is proven
// against the same validation production uses.
func TestPrismProvider_GoldenThroughTheSeam(t *testing.T) {
	requirePrism(t)
	repo := writePrismFixture(t)
	ff, records := Run(context.Background(), []Provider{{
		Name:            "prism",
		Command:         []string{"ruby", prismScript(t)},
		ExpectedVersion: "0.1.0",
	}}, repo, nil, nil)
	if len(records) != 1 || records[0].Skipped {
		t.Fatalf("census = %+v, want a clean run", records)
	}
	if records[0].Version != "0.1.0" {
		t.Errorf("reported version = %q", records[0].Version)
	}

	want := []struct{ name, level string }{
		{"prism-call: Billing#charge -> Billing#notify", "lexical-self"},
		{"prism-call: Billing#charge -> Billing#validate", "lexical-self"},
		{"prism-call: Billing#charge -> Ledger#record", "constant-receiver"},
		{"prism-call: Billing#charge -> settle", "name-only"},
	}
	if len(ff) != len(want) {
		t.Fatalf("facts = %d, want %d: %+v", len(ff), len(want), ff)
	}
	for i, w := range want {
		f := ff[i]
		if f.Name != w.name || f.Props[PropResolutionLevel] != w.level {
			t.Errorf("fact[%d] = %q (%v), want %q (%s)", i, f.Name, f.Props[PropResolutionLevel], w.name, w.level)
		}
		if f.Kind != "dependency" || f.File != "app/billing.rb" {
			t.Errorf("fact[%d] identity = %s %s", i, f.Kind, f.File)
		}
		if len(f.Relations) != 1 || f.Relations[0].Kind != "calls" {
			t.Errorf("fact[%d] relations = %+v, want one calls edge", i, f.Relations)
		}
	}
	for _, f := range ff {
		if strings.Contains(f.Name, "Secret") {
			t.Errorf("vendored tree must be skipped, got %q", f.Name)
		}
	}
}

func TestPrismProvider_OutputIsSortedAndDeterministic(t *testing.T) {
	requirePrism(t)
	repo := writePrismFixture(t)
	script := prismScript(t)
	run := func() []byte {
		out, err := exec.Command("ruby", script, repo).Output()
		if err != nil {
			t.Fatalf("provider run failed: %v", err)
		}
		return out
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

// A `new` on a literal constant is an instantiation, not a call, and when its
// result is immediately the receiver of another call the ceremony is named on
// the fact. A `new` on anything the parser cannot name stays a call.
func TestPrismProvider_EmitsInstantiations(t *testing.T) {
	requirePrism(t)
	repo := t.TempDir()
	src := "class Checkout\n" +
		"  def run(order)\n" +
		"    Payments::Charge.new(order).call\n" +
		"    receipt = Receipt.new(order)\n" +
		"    receipt.deliver\n" +
		"    klass.new(order).call\n" +
		"  end\n" +
		"end\n"
	if err := os.MkdirAll(filepath.Join(repo, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "checkout.rb"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(context.Background(), "ruby", prismScript(t), repo)
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	var news []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.Contains(line, "prism-new:") {
			news = append(news, line)
		}
	}
	sort.Strings(news)
	if len(news) != 2 {
		t.Fatalf("want two instantiations (the two literal constants), got %d: %v", len(news), news)
	}
	joined := strings.Join(news, "\n")
	for _, want := range []string{
		`"name":"prism-new: Checkout#run -> Payments::Charge"`,
		`"one_shot_call":"call"`,
		`"name":"prism-new: Checkout#run -> Receipt"`,
		`"kind":"instantiates"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in:\n%s", want, joined)
		}
	}
	if strings.Count(joined, "one_shot_call") != 1 {
		t.Errorf("only the chained instantiation carries the ceremony:\n%s", joined)
	}
}
