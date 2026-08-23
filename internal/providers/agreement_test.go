package providers

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func callLine(name, file string, line int, target string) string {
	return `{"kind":"dependency","name":"` + name + `","file":"` + file + `","line":` + itoa(line) +
		`,"props":{"resolution_level":"constant-receiver"},"relations":[{"kind":"calls","target":"` + target + `"}]}`
}

func itoa(n int) string { return strconv.Itoa(n) }

func runTwo(t *testing.T, prism, rubydex string) ([]facts.Fact, []facts.ProviderRecord) {
	t.Helper()
	a := writeProvider(t, "0.1.0", prism+"\n")
	b := writeProvider(t, "0.1.0", rubydex+"\n")
	return Run(context.Background(), []Provider{
		{Name: "prism", Command: []string{a}},
		{Name: "rubydex", Command: []string{b}},
	}, t.TempDir(), nil, nil)
}

func byName(ff []facts.Fact) map[string]facts.Fact {
	out := map[string]facts.Fact{}
	for _, f := range ff {
		out[f.Name] = f
	}
	return out
}

func TestPairing_SingletonSpelledBothWaysIsOneEdge(t *testing.T) {
	ff, records := runTwo(t,
		callLine("prism-call: ReminderJob#perform -> Board#find", "app/jobs/reminder_job.rb", 3, "Board#find"),
		callLine("rubydex-call: ReminderJob#perform -> Board.find", "app/jobs/reminder_job.rb", 3, "Board.find"))
	if len(ff) != 1 {
		t.Fatalf("one call site read by two producers must be one relation, got %d: %+v", len(ff), ff)
	}
	f := ff[0]
	if f.Props[PropProvider] != "prism" {
		t.Errorf("the survivor is the first producer in name order, got %v", f.Props[PropProvider])
	}
	if got := f.Relations[0].Target; got != "Board.find" {
		t.Errorf("the survivor takes the scope-bearing spelling, got %q", got)
	}
	if f.Name != "prism-call: ReminderJob#perform -> Board.find" {
		t.Errorf("the survivor's name follows its callee, got %q", f.Name)
	}
	if f.Props[PropResolutionAgreement] != AgreementLevel {
		t.Errorf("an agreed relation carries %s=%s, got %v", PropResolutionAgreement, AgreementLevel, f.Props)
	}
	if f.Props[PropResolutionLevel] != LevelConstantReceiver {
		t.Errorf("the producer's own resolution_level is never rewritten, got %v", f.Props[PropResolutionLevel])
	}
	for _, r := range records {
		if r.Agreed != 1 || r.Differing != 0 || r.OneSided != 0 {
			t.Errorf("%s: agreed=%d differing=%d one_sided=%d, want 1/0/0", r.Name, r.Agreed, r.Differing, r.OneSided)
		}
	}
}

func TestPairing_SingletonMarkerIsNormalisedBeforePairing(t *testing.T) {
	ff, records := runTwo(t,
		callLine("prism-call: Setup#run -> Object#puts", "lib/setup.rb", 9, "Object#puts"),
		callLine("rubydex-call: Setup#run -> <Object>.puts", "lib/setup.rb", 9, "<Object>.puts"))
	if len(ff) != 1 || ff[0].Relations[0].Target != "Object.puts" {
		t.Fatalf("the marker spelling must pair with the plain one, got %+v", ff)
	}
	for _, r := range records {
		if r.Differing != 0 || len(r.Differences) != 0 {
			t.Errorf("%s: a spelling the table covers is not a difference: %+v", r.Name, r.Differences)
		}
	}
}

func TestPairing_DifferentReceiversAreBothKeptAndCounted(t *testing.T) {
	ff, records := runTwo(t,
		callLine("prism-call: Parser#parse -> HTML#fragment", "lib/parser.rb", 4, "HTML#fragment"),
		callLine("rubydex-call: Parser#parse -> HTML4.fragment", "lib/parser.rb", 4, "HTML4.fragment"))
	if len(ff) != 2 {
		t.Fatalf("a difference is a count, never a vote: both relations stay, got %d", len(ff))
	}
	for _, r := range records {
		if r.Agreed != 0 || r.Differing != 1 || r.OneSided != 0 {
			t.Errorf("%s: agreed=%d differing=%d one_sided=%d, want 0/1/0", r.Name, r.Agreed, r.Differing, r.OneSided)
		}
		if len(r.Differences) != 1 || r.Differences[0].Cause != DifferenceDiffering || r.Differences[0].Count != 1 {
			t.Fatalf("%s: differences = %+v", r.Name, r.Differences)
		}
		ex := r.Differences[0].Examples[0]
		if !strings.Contains(ex, "prism=HTML#fragment") || !strings.Contains(ex, "rubydex=HTML4.fragment") {
			t.Errorf("the evidence names both spellings, got %q", ex)
		}
	}
}

func TestPairing_AliasResolvedIsNamedWhenTheProducerSaysSo(t *testing.T) {
	alias := `{"kind":"dependency","name":"rubydex-read: lib/parser.rb:4 HTML","file":"lib/parser.rb","line":4,"props":{"resolution_level":"resolved","resolution_cause":"alias"}}`
	_, records := runTwo(t,
		callLine("prism-call: Parser#parse -> HTML#fragment", "lib/parser.rb", 4, "HTML#fragment"),
		callLine("rubydex-call: Parser#parse -> HTML4.fragment", "lib/parser.rb", 4, "HTML4.fragment")+"\n"+alias)
	if len(records[0].Differences) != 1 || records[0].Differences[0].Cause != DifferenceAliasResolved {
		t.Errorf("a receiver the engine resolved through an alias is alias-resolved, got %+v (rubydex record: %+v)", records[0].Differences, records[1])
	}
}

func TestPairing_OneSidedReadsAreCountedNotTouched(t *testing.T) {
	ff, records := runTwo(t,
		callLine("prism-call: A#m -> B#n", "a.rb", 1, "B#n"),
		callLine("rubydex-call: A#m -> C.o", "a.rb", 2, "C.o"))
	if len(ff) != 2 {
		t.Fatalf("facts only one producer emitted merge as before, got %d", len(ff))
	}
	for _, r := range records {
		if r.OneSided != 1 || r.Agreed != 0 || r.Differing != 0 {
			t.Errorf("%s: one_sided=%d agreed=%d differing=%d, want 1/0/0", r.Name, r.OneSided, r.Agreed, r.Differing)
		}
	}
}

func TestPairing_TwoCallsOfOneMethodOnALinePairOneToOne(t *testing.T) {
	ff, records := runTwo(t,
		callLine("prism-call: A#m -> B#n", "a.rb", 1, "B#n")+"\n"+callLine("prism-call: A#m -> B#n (2)", "a.rb", 1, "B#n"),
		callLine("rubydex-call: A#m -> B#n", "a.rb", 1, "B#n"))
	if len(ff) != 2 {
		t.Fatalf("one pair and one one-sided read, got %d: %+v", len(ff), byName(ff))
	}
	if records[0].Agreed != 1 || records[0].OneSided != 1 || records[1].Agreed != 1 || records[1].OneSided != 0 {
		t.Errorf("records = %+v", records)
	}
}

func TestPairing_SingleProviderIsUntouched(t *testing.T) {
	script := writeProvider(t, "0.1.0", callLine("prism-call: A#m -> B#n", "a.rb", 1, "B#n")+"\n")
	ff, records := Run(context.Background(), []Provider{{Name: "prism", Command: []string{script}}}, t.TempDir(), nil, nil)
	if len(ff) != 1 || ff[0].Props[PropResolutionAgreement] != nil || records[0].OneSided != 0 {
		t.Errorf("with one producer nothing is paired and nothing is one-sided: %+v %+v", ff, records)
	}
}
