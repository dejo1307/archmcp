package mining

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/enola-labs/enola/internal/explainers/constraints"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

func classSymbol(name, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Props: map[string]any{"symbol_kind": facts.SymbolClass}}
}

func methodSymbol(name, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindSymbol, Name: name, File: file, Props: map[string]any{"symbol_kind": facts.SymbolMethod}}
}

func namingWorld() []facts.Fact {
	words := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel", "India", "Juliett", "Kilo", "Lima"}
	var ff []facts.Fact
	for _, w := range words {
		ff = append(ff, classSymbol(w+"Serializer", "app/serializers/"+strings.ToLower(w)+"_serializer.rb"))
	}
	ff = append(ff, classSymbol("Widget", "app/serializers/widget.rb"))
	return ff
}

func edgeWorld() []facts.Fact {
	var ff []facts.Fact
	for i := 0; i < 19; i++ {
		ff = append(ff, classSymbol(fmt.Sprintf("Model%02d", i), fmt.Sprintf("app/models/model_%02d.rb", i)))
	}
	ff = append(ff, classSymbol("JobX", "app/jobs/job_x.rb"), methodSymbol("JobX#perform", "app/jobs/job_x.rb"))
	for i := 0; i < 20; i++ {
		svc := classSymbol(fmt.Sprintf("Svc%02d", i), fmt.Sprintf("app/services/svc_%02d.rb", i))
		target := fmt.Sprintf("Model%02d", i)
		if i == 19 {
			target = "JobX"
		}
		svc.Relations = []facts.Relation{{Kind: facts.RelDependsOn, Target: target}}
		ff = append(ff, svc)
	}
	return ff
}

func methodWorld() []facts.Fact {
	var ff []facts.Fact
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("Work%d", i)
		file := fmt.Sprintf("app/jobs/work_%d.rb", i)
		ff = append(ff, classSymbol(name, file), methodSymbol(name+"#perform", file))
	}
	ff = append(ff, classSymbol("Idle", "app/jobs/idle.rb"))
	for i := 0; i < 3; i++ {
		ff = append(ff, classSymbol(fmt.Sprintf("Helper%d", i), fmt.Sprintf("lib/util/helper_%d.rb", i)))
	}
	return ff
}

func fullWorld() []facts.Fact {
	var ff []facts.Fact
	ff = append(ff, companyFKStore()...)
	ff = append(ff, namingWorld()...)
	ff = append(ff, edgeWorld()...)
	ff = append(ff, methodWorld()...)
	return ff
}

func TestMineCoversEveryFamilyOnTheFullWorld(t *testing.T) {
	report := Mine(storeOf(fullWorld()), DefaultConfig())
	findCandidate(t, report, FamilyPropImplication, "company_id->companies")
	naming := findCandidate(t, report, FamilyNaming, "*Serializer")
	if naming.Numerator != 12 || naming.Denominator != 13 {
		t.Errorf("naming regularity = %d/%d, want 12/13", naming.Numerator, naming.Denominator)
	}
	if len(naming.Exceptions) != 1 || naming.Exceptions[0].Name != "Widget" {
		t.Errorf("naming exceptions = %v, want exactly Widget", naming.Exceptions)
	}
	forbid := findCandidate(t, report, FamilyForbidEdge, "land outside app/jobs/")
	if forbid.Numerator != 19 || forbid.Denominator != 20 {
		t.Errorf("forbid regularity = %d/%d, want 19/20", forbid.Numerator, forbid.Denominator)
	}
	if len(forbid.Exceptions) != 1 || forbid.Exceptions[0].Name != "Svc19 -> JobX" {
		t.Errorf("forbid exceptions = %v, want exactly Svc19 -> JobX", forbid.Exceptions)
	}
	allow := findCandidate(t, report, FamilyAllowOnly, "land in app/models")
	if allow.Numerator != 19 || allow.Denominator != 20 {
		t.Errorf("allow regularity = %d/%d, want 19/20", allow.Numerator, allow.Denominator)
	}
	defines := findCandidate(t, report, FamilyMethodPresence, "define perform")
	if defines.Numerator != 11 || defines.Denominator != 12 {
		t.Errorf("defines regularity = %d/%d, want 11/12", defines.Numerator, defines.Denominator)
	}
	if len(defines.Exceptions) != 1 || defines.Exceptions[0].Name != "Idle" {
		t.Errorf("defines exceptions = %v, want exactly Idle", defines.Exceptions)
	}
}

func TestEveryCandidateRoundTripsThroughTheRealParser(t *testing.T) {
	report := Mine(storeOf(fullWorld()), DefaultConfig())
	if len(report.Candidates) == 0 {
		t.Fatal("no candidates mined from the full world")
	}
	for _, c := range report.Candidates {
		var file intent.ConstraintsFile
		if err := yaml.Unmarshal([]byte(c.YAML), &file); err != nil {
			t.Fatalf("candidate %s YAML does not parse: %v\n%s", c.Rule.ID, err, c.YAML)
		}
		d := &intent.Declaration{Components: file.Components, Rules: file.Rules}
		if problems := d.Problems(); len(problems) > 0 {
			t.Errorf("candidate %s fails validation: %v\n%s", c.Rule.ID, problems, c.YAML)
		}
		if len(file.Rules) != 1 || file.Rules[0].ID != c.Rule.ID {
			t.Errorf("candidate %s YAML carries rules %v, want exactly its own", c.Rule.ID, len(file.Rules))
		}
		if file.Rules[0].Mode != "advisory" {
			t.Errorf("candidate %s proposes mode %q, want advisory", c.Rule.ID, file.Rules[0].Mode)
		}
		if file.Rules[0].Because == "" {
			t.Errorf("candidate %s proposes an empty because", c.Rule.ID)
		}
		if len(file.Rules[0].Exempt) != 0 {
			t.Errorf("candidate %s proposes exemptions %v, want none: mining reports reality, and a carve-out is a decision only an operator signs", c.Rule.ID, file.Rules[0].Exempt)
		}
	}
}

func TestCandidateExceptionsAreExactlyTheWouldBeViolations(t *testing.T) {
	world := fullWorld()
	report := Mine(storeOf(world), DefaultConfig())
	if len(report.Candidates) == 0 {
		t.Fatal("no candidates mined from the full world")
	}
	for _, c := range report.Candidates {
		t.Run(c.Rule.ID, func(t *testing.T) {
			var file intent.ConstraintsFile
			if err := yaml.Unmarshal([]byte(c.YAML), &file); err != nil {
				t.Fatal(err)
			}
			d := &intent.Declaration{Components: file.Components, Rules: file.Rules, Source: "mined"}
			if problems := d.Problems(); len(problems) > 0 {
				t.Fatal(problems)
			}
			store := storeOf(world)
			store.Add(intent.CompileFacts(d)...)
			insights, err := constraints.New().Explain(context.Background(), store)
			if err != nil {
				t.Fatal(err)
			}
			marker := "constraint " + c.Rule.ID + " violated"
			var violations []string
			for _, in := range insights {
				if strings.Contains(in.Title, marker) {
					violations = append(violations, in.Title)
				}
			}
			if len(violations) != len(c.Exceptions) {
				t.Fatalf("would-be violations = %d, mined exceptions = %d\nviolations:\n%s",
					len(violations), len(c.Exceptions), strings.Join(violations, "\n"))
			}
			for _, e := range c.Exceptions {
				named := false
				for _, title := range violations {
					if strings.Contains(title, e.Name) {
						named = true
						break
					}
				}
				if !named {
					t.Errorf("exception %s is not among the would-be violations", e.Name)
				}
			}
		})
	}
}

func TestMineFromJSONLFixtureEndToEnd(t *testing.T) {
	store := storeOf(fullWorld())
	var jsonl strings.Builder
	if err := store.WriteJSONL(&jsonl); err != nil {
		t.Fatal(err)
	}
	loaded := facts.NewStore()
	if err := loaded.ReadJSONL(strings.NewReader(jsonl.String())); err != nil {
		t.Fatal(err)
	}
	report := Mine(loaded, DefaultConfig())
	c := findCandidate(t, report, FamilyPropImplication, "columns contains company_id also have fk_constraints containing company_id->companies")
	if c.Numerator != 20 || c.Denominator != 22 || len(c.Exceptions) != 2 {
		t.Errorf("fixture round-trip changed the regularity: %d/%d with %d exceptions", c.Numerator, c.Denominator, len(c.Exceptions))
	}
}
