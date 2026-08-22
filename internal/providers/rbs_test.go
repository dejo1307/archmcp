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

	"github.com/enola-labs/enola/internal/facts"
)

const validDeclaredFact = `{"kind":"symbol","name":"rbs-signature: Billing::Ledger#record","file":"sig/billing.rbs","props":{"resolution_level":"declared","declared_in":"sig/billing.rbs","syntax":"rbs","receiver":"Billing::Ledger","method":"record","singleton":false,"signature":"(Invoice invoice) -> String"},"relations":[{"kind":"has_method","target":"Billing::Ledger#record"}]}`

func TestParseFactLine_DeclaredIsInTheVocabulary(t *testing.T) {
	f, err := parseFactLine(validDeclaredFact)
	if err != nil {
		t.Fatalf("a declared fact naming its signature file must validate, got %v", err)
	}
	if f.Props[PropResolutionLevel] != LevelDeclared || f.Props[PropDeclaredIn] != "sig/billing.rbs" {
		t.Errorf("props = %+v", f.Props)
	}
}

func TestParseFactLine_DeclaredWithoutSignatureFileIsRejected(t *testing.T) {
	_, err := parseFactLine(`{"kind":"symbol","name":"rbs-signature: Foo#bar","props":{"resolution_level":"declared"}}`)
	if err == nil || !strings.Contains(err.Error(), PropDeclaredIn) {
		t.Fatalf("err = %v, want a rejection naming the missing signature file prop", err)
	}
}

func TestParseCensus_OneStrictLine(t *testing.T) {
	census, err := parseCensus([]byte("some warning\n" + CensusPrefix +
		`{"files_seen":3,"declarations_parsed":11,"constructs_skipped":7,"skip_causes":[{"cause":"rbs-attribute","count":4},{"cause":"rbs-mixin","count":3}]}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if census == nil || census.FilesSeen != 3 || census.DeclarationsParsed != 11 || census.ConstructsSkipped != 7 {
		t.Fatalf("census = %+v", census)
	}
	if len(census.SkipCauses) != 2 || census.SkipCauses[0].Cause != "rbs-attribute" || census.SkipCauses[0].Count != 4 {
		t.Errorf("skip causes = %+v", census.SkipCauses)
	}
}

func TestParseCensus_AbsentLineIsANilCensus(t *testing.T) {
	census, err := parseCensus([]byte("zeitwerk-map: skipped 3 file(s)\n"))
	if err != nil || census != nil {
		t.Fatalf("census = %+v, err = %v, want nil census and no error", census, err)
	}
}

func TestParseCensus_MalformedOrDoubledLinesAreNamedErrors(t *testing.T) {
	if _, err := parseCensus([]byte(CensusPrefix + "not json\n")); err == nil {
		t.Error("malformed census accounting must be rejected")
	}
	if _, err := parseCensus([]byte(CensusPrefix + `{"files_seen":1,"surprise":true}` + "\n")); err == nil {
		t.Error("census accounting with unknown fields must be rejected")
	}
	line := CensusPrefix + `{"files_seen":1,"declarations_parsed":0,"constructs_skipped":0}` + "\n"
	if _, err := parseCensus([]byte(line + line)); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("err = %v, want a rejection of the doubled accounting", err)
	}
}

func TestRun_InvalidCensusSkipsTheWholeOutput(t *testing.T) {
	script := filepath.Join(t.TempDir(), "provider")
	body := "#!/bin/sh\n" +
		"for a in \"$@\"; do if [ \"$a\" = \"--version\" ]; then echo 1.0.0; exit 0; fi; done\n" +
		`echo '{"kind":"symbol","name":"x","props":{"resolution_level":"name-only"}}'` + "\n" +
		`echo '` + CensusPrefix + `broken' >&2` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ff, records := Run(context.Background(), []Provider{{Name: "broken-census", Command: []string{script}}}, t.TempDir(), nil, nil)
	if len(ff) != 0 || len(records) != 1 || !records[0].Skipped || !strings.Contains(records[0].Reason, "invalid census") {
		t.Fatalf("facts = %+v, census = %+v, want a named census rejection", ff, records)
	}
}

func declaredContractFact(receiver, method, signature, file string, singleton bool) facts.Fact {
	separator := "#"
	if singleton {
		separator = "."
	}
	return facts.Fact{Kind: facts.KindSymbol, Name: "rbs-signature: " + receiver + separator + method,
		File: file,
		Props: map[string]any{PropResolutionLevel: LevelDeclared, PropDeclaredIn: file,
			"receiver": receiver, "method": method, "singleton": singleton, "signature": signature}}
}

func TestLinkDeclaredContracts_StampsTheMatchingExtractedSymbol(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing::Ledger#record", File: "app/models/billing/ledger.rb",
			Props: map[string]any{"symbol_kind": "method", "language": "ruby"}},
		facts.Fact{Kind: facts.KindSymbol, Name: "Billing::Ledger#void", File: "app/models/billing/ledger.rb",
			Props: map[string]any{"symbol_kind": "method", "language": "ruby"}},
		declaredContractFact("Billing::Ledger", "record", "(Invoice invoice) -> String", "sig/billing.rbs", false),
	)
	if n := LinkDeclaredContracts(store, 0); n != 1 {
		t.Fatalf("annotated = %d, want 1", n)
	}
	byName := map[string]facts.Fact{}
	for _, f := range store.FactsRef() {
		byName[f.Name] = f
	}
	got := byName["Billing::Ledger#record"]
	if got.Props[PropTyped] != true ||
		got.Props[PropDeclaredSignature] != "(Invoice invoice) -> String" ||
		got.Props[PropDeclaredIn] != "sig/billing.rbs" {
		t.Errorf("stamped symbol props = %+v", got.Props)
	}
	if got.Props["symbol_kind"] != "method" || got.File != "app/models/billing/ledger.rb" {
		t.Errorf("extractor identity must survive the stamp: %+v", got)
	}
	unmatched := byName["Billing::Ledger#void"]
	if _, claimed := unmatched.Props[PropTyped]; claimed {
		t.Errorf("a symbol no declaration covers must stay unstamped: %+v", unmatched.Props)
	}
	declaration := byName["rbs-signature: Billing::Ledger#record"]
	if _, claimed := declaration.Props[PropTyped]; claimed {
		t.Errorf("the declaration itself must never be stamped: %+v", declaration.Props)
	}
}

func TestLinkDeclaredContracts_SingletonAndInstanceAreDistinctIdentities(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "Order#place", File: "app/models/order.rb",
			Props: map[string]any{}},
		declaredContractFact("Order", "place", "() -> Order", "sorbet/rbi/order.rbi", true),
	)
	if n := LinkDeclaredContracts(store, 0); n != 0 {
		t.Fatalf("annotated = %d, want 0: a singleton declaration is no claim about the instance method", n)
	}
}

func TestLinkDeclaredContracts_SignaturesAndFilesMergeSortedAndIdempotently(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindSymbol, Name: "Order#total", File: "app/models/order.rb",
			Props: map[string]any{}},
		declaredContractFact("Order", "total", "() -> Money", "sig/order.rbs", false),
		declaredContractFact("Order", "total", "() -> Integer", "sorbet/rbi/order.rbi", false),
	)
	LinkDeclaredContracts(store, 0)
	LinkDeclaredContracts(store, 0)
	var got facts.Fact
	for _, f := range store.FactsRef() {
		if f.Name == "Order#total" {
			got = f
		}
	}
	if got.Props[PropDeclaredSignature] != "() -> Integer | () -> Money" {
		t.Errorf("declared_signature = %q, want the sorted merged signature set", got.Props[PropDeclaredSignature])
	}
	if got.Props[PropDeclaredIn] != "sig/order.rbs sorbet/rbi/order.rbi" {
		t.Errorf("declared_in = %q, want the sorted merged file set", got.Props[PropDeclaredIn])
	}
}

func TestLinkDeclaredContracts_StaysInsideTheWindow(t *testing.T) {
	store := facts.NewStore()
	store.Add(facts.Fact{Kind: facts.KindSymbol, Name: "Order#total", File: "app/models/order.rb", Repo: "earlier",
		Props: map[string]any{}})
	windowStart := store.Count()
	store.Add(declaredContractFact("Order", "total", "() -> Money", "sig/order.rbs", false))
	if n := LinkDeclaredContracts(store, windowStart); n != 0 {
		t.Fatalf("annotated = %d, want 0: an earlier repo's symbol is outside this declaration's window", n)
	}
	if _, claimed := store.FactsRef()[0].Props[PropTyped]; claimed {
		t.Error("a fact before the window start was stamped")
	}
}

func rbsScript(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "examples", "providers", "ruby", "rbs", "enola_rbs_provider.rb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("rbs provider missing: %v", err)
	}
	return path
}

func writeRbsFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	files := map[string]string{
		"sig/billing.rbs": "class Billing::Ledger[T] < Base\n" +
			"  def record: (Invoice invoice, ?Integer retries) -> String\n" +
			"            | (Invoice invoice) { (String) -> void } -> String\n" +
			"  def self.open: (untyped config) -> instance\n" +
			"  attr_reader balance: Integer\n" +
			"  include Auditable\n" +
			"end\n" +
			"\n" +
			"interface _Chargeable\n" +
			"  def charge: (Money amount) -> bool\n" +
			"end\n",
		"sorbet/rbi/models.rbi": "class Order < ApplicationRecord\n" +
			"  sig { params(items: T::Array[Item], notify: T.untyped).returns(Order) }\n" +
			"  def self.place(items, notify); end\n" +
			"\n" +
			"  sig { returns(String) }\n" +
			"  def reference; end\n" +
			"\n" +
			"  def untracked; end\n" +
			"\n" +
			"  sig { mystery.returns(String) }\n" +
			"  def opaque; end\n" +
			"end\n",
		"app/services/charger.rb": "module Billing\n" +
			"  class Charger\n" +
			"    sig { params(amount: Integer).void }\n" +
			"    def charge(amount)\n" +
			"      ledger.record(amount)\n" +
			"    end\n" +
			"  end\n" +
			"end\n",
		"vendor/sig/hidden.rbs": "class Hidden\n  def leak: () -> void\nend\n",
	}
	for name, content := range files {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestRbsProvider_GoldenThroughTheSeam(t *testing.T) {
	requireRuby(t)
	repo := writeRbsFixture(t)
	ff, records := Run(context.Background(), []Provider{{
		Name:            "rbs",
		Command:         []string{"ruby", rbsScript(t)},
		ExpectedVersion: "0.1.0",
	}}, repo, nil, nil)
	if len(records) != 1 || records[0].Skipped {
		t.Fatalf("census = %+v, want a clean run", records)
	}
	if records[0].Version != "0.1.0" {
		t.Errorf("reported version = %q", records[0].Version)
	}

	byName := map[string]facts.Fact{}
	for _, f := range ff {
		if f.Props[PropResolutionLevel] != LevelDeclared {
			t.Errorf("fact %q level = %v, want %s", f.Name, f.Props[PropResolutionLevel], LevelDeclared)
		}
		if f.Props[PropDeclaredIn] != f.File {
			t.Errorf("fact %q declared_in = %v, want its own signature file %s", f.Name, f.Props[PropDeclaredIn], f.File)
		}
		if f.Props[PropProvider] != "rbs" || f.Props[PropProviderVersion] != "0.1.0" {
			t.Errorf("fact %q not stamped with provenance: %+v", f.Name, f.Props)
		}
		byName[f.Name] = f
	}

	overloaded := byName["rbs-signature: Billing::Ledger#record"]
	if overloaded.Kind != facts.KindSymbol || overloaded.File != "sig/billing.rbs" ||
		overloaded.Props["signature"] != "(Invoice invoice, ?Integer retries) -> String | (Invoice invoice) { (String) -> void } -> String" ||
		overloaded.Props["overload_count"] != float64(2) {
		t.Errorf("overloaded contract = %+v", overloaded)
	}
	if len(overloaded.Relations) != 1 || overloaded.Relations[0].Kind != facts.RelHasMethod ||
		overloaded.Relations[0].Target != "Billing::Ledger#record" {
		t.Errorf("overloaded contract relations = %+v", overloaded.Relations)
	}
	singleton := byName["rbs-signature: Billing::Ledger.open"]
	if singleton.Props["singleton"] != true || singleton.Props["return_type"] != "instance" {
		t.Errorf("singleton contract = %+v", singleton)
	}
	if params, _ := singleton.Props["params"].([]any); len(params) != 1 || params[0] != "untyped config" {
		t.Errorf("untyped param must be recorded, not omitted: %+v", singleton.Props["params"])
	}
	iface := byName["rbs-decl: _Chargeable"]
	if iface.Props["decl_kind"] != "interface" {
		t.Errorf("interface declaration = %+v", iface)
	}
	generic := byName["rbs-decl: Billing::Ledger"]
	if generic.Props["superclass"] != "Base" {
		t.Errorf("generic class declaration = %+v", generic)
	}
	if tp, _ := generic.Props["type_params"].([]any); len(tp) != 1 || tp[0] != "T" {
		t.Errorf("type params = %+v", generic.Props["type_params"])
	}
	sorbet := byName["rbs-signature: Order.place"]
	if sorbet.Props["syntax"] != "sorbet-rbi" || sorbet.Props["return_type"] != "Order" {
		t.Errorf("sorbet contract = %+v", sorbet)
	}
	if params, _ := sorbet.Props["params"].([]any); len(params) != 2 || params[1] != "notify: T.untyped" {
		t.Errorf("T.untyped param must be recorded, not omitted: %+v", sorbet.Props["params"])
	}
	inline := byName["rbs-signature: Billing::Charger#charge"]
	if inline.Props["syntax"] != "sorbet-sig" || inline.Props["return_type"] != "void" ||
		inline.File != "app/services/charger.rb" {
		t.Errorf("inline sig contract = %+v", inline)
	}
	for _, f := range ff {
		if strings.Contains(f.Name, "Hidden") {
			t.Errorf("vendored tree must be skipped, got %q", f.Name)
		}
		if strings.Contains(f.Name, "opaque") || strings.Contains(f.Name, "untracked") {
			t.Errorf("an unparsed or untyped construct must not become a fact: %q", f.Name)
		}
	}

	census := records[0].Census
	if census == nil {
		t.Fatal("the provider must report its coverage accounting")
	}
	if census.FilesSeen != 3 || census.DeclarationsParsed != len(ff) {
		t.Errorf("census = %+v over %d facts", census, len(ff))
	}
	causes := map[string]int{}
	for _, c := range census.SkipCauses {
		causes[c.Cause] = c.Count
	}
	if causes["rbs-attribute"] != 1 || causes["rbs-mixin"] != 1 ||
		causes["sig-link-mystery"] != 1 || causes["rbi-def-without-sig"] != 2 {
		t.Errorf("skip causes = %+v", causes)
	}
	total := 0
	for _, c := range census.SkipCauses {
		total += c.Count
	}
	if census.ConstructsSkipped != total {
		t.Errorf("constructs_skipped = %d, causes sum to %d", census.ConstructsSkipped, total)
	}
}

func TestRbsProvider_EmptyRepositoryIsAnEmptyContribution(t *testing.T) {
	requireRuby(t)
	repo := t.TempDir()
	ff, records := Run(context.Background(), []Provider{{
		Name:    "rbs",
		Command: []string{"ruby", rbsScript(t)},
	}}, repo, nil, nil)
	if len(ff) != 0 || len(records) != 1 || records[0].Skipped || records[0].FactCount != 0 {
		t.Fatalf("facts = %+v, census = %+v, want a clean zero-fact run", ff, records)
	}
	if records[0].Census == nil || records[0].Census.FilesSeen != 0 {
		t.Fatalf("census = %+v, want zero files seen stated rather than absent", records[0].Census)
	}
}

func TestRbsProvider_UnbalancedSignatureFileIsDiscardedWhole(t *testing.T) {
	requireRuby(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "sig"), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := "class Billing\n  def charge: (Money amount) -> bool\n"
	if err := os.WriteFile(filepath.Join(repo, "sig", "broken.rbs"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	ff, records := Run(context.Background(), []Provider{{
		Name:    "rbs",
		Command: []string{"ruby", rbsScript(t)},
	}}, repo, nil, nil)
	if len(ff) != 0 {
		t.Fatalf("facts = %+v, want none: a structurally broken signature file must not become partial truth", ff)
	}
	census := records[0].Census
	if census == nil || census.DeclarationsParsed != 0 {
		t.Fatalf("census = %+v, want the discarded declarations retracted from the parsed count", census)
	}
	causes := map[string]int{}
	for _, c := range census.SkipCauses {
		causes[c.Cause] = c.Count
	}
	if causes["rbs-file-unbalanced"] != 1 {
		t.Errorf("skip causes = %+v, want the discard named", causes)
	}
}

func TestRbsProvider_OutputIsSortedAndDeterministic(t *testing.T) {
	requireRuby(t)
	repo := writeRbsFixture(t)
	script := rbsScript(t)
	run := func() []byte {
		cmd := exec.Command("ruby", script, repo)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("provider run failed: %v (%s)", err, stderr.String())
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
