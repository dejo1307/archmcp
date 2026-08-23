package check

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func placedInsight(source, title string, file string, line int) facts.Insight {
	in := insight(source, title, 1.0)
	in.Description = "x must not reach y. The rule is declared. Because: a model that knows the request cannot be used off it"
	in.Evidence = []facts.Evidence{{Fact: "rule: models-never-render", Detail: "declared in enola/constraints/rails.rb"},
		{File: file, Symbol: "User#render", Detail: "User#render -> render via calls", Line: line, EndLine: line, Column: 5, EndColumn: 11}}
	in.Actions = []string{"Move the render into a presenter the controller owns"}
	return in
}

func formatVerdict() Verdict {
	failing := placedInsight("constraints", "Constraint models-never-render violated: User#render -> render via calls", "app/models/user.rb", 12)
	advisory := placedInsight("constraints", "Advisory constraint jobs-no-render violated: Job#perform -> render via calls", "app/jobs/job.rb", 3)
	advisory.Confidence = 0.9
	suppressed := placedInsight("constraints", "Constraint models-never-render violated: Admin#render -> render via calls", "app/models/admin.rb", 7)
	resolved := placedInsight("constraints", "Constraint models-never-render violated: Old#render -> render via calls", "app/models/old.rb", 2)
	unplaced := insight("cycles", "Dependency cycle: 3 modules", 1.0)
	unplaced.Evidence = []facts.Evidence{{Fact: "app/jobs -> app/models", Detail: "cycle"}}
	unplaced.Description = "The modules reach each other."
	return Verdict{
		Status: StatusRegression,
		Policy: Policy{FailExplainers: []string{"constraints"}, Suppressions: []Suppression{{
			FindingTitlePrefix: "Constraint models-never-render violated: Admin#render", Owner: "dana", Reason: "legacy admin, scheduled for removal", Date: "2026-08-01",
		}}},
		Failures:   []facts.Insight{failing},
		Advisories: []facts.Insight{advisory, unplaced},
		Suppressed: []facts.Insight{suppressed},
		Resolved:   []facts.Insight{resolved},
	}
}

func TestSARIF_ShapeAndContents(t *testing.T) {
	out, err := formatVerdict().SARIF()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema  string `json:"$schema"`
		Version string
		Runs    []struct {
			Tool struct {
				Driver struct {
					Name  string
					Rules []struct {
						ID               string
						ShortDescription struct{ Text string }
					}
				}
			}
			Results []struct {
				RuleID    string
				RuleIndex int
				Level     string
				Message   struct{ Text string }
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct{ URI string }
						Region           struct{ StartLine, StartColumn, EndLine, EndColumn int }
					}
				}
				PartialFingerprints map[string]string
				Suppressions        []struct{ Kind, Justification string }
				Properties          struct {
					Bucket, Policy, Source, SuggestedAction string
					Confidence                              float64
				}
			}
		}
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Version != "2.1.0" || !strings.Contains(doc.Schema, "sarif-2.1.0") || len(doc.Runs) != 1 {
		t.Fatalf("not one SARIF 2.1.0 run: version %q schema %q runs %d", doc.Version, doc.Schema, len(doc.Runs))
	}
	run := doc.Runs[0]
	if run.Tool.Driver.Name != "enola" {
		t.Fatalf("driver = %q", run.Tool.Driver.Name)
	}
	rules := map[string]string{}
	for _, r := range run.Tool.Driver.Rules {
		rules[r.ID] = r.ShortDescription.Text
	}
	if rules["models-never-render"] != "a model that knows the request cannot be used off it" {
		t.Fatalf("rule reason = %q, want the declared because", rules["models-never-render"])
	}
	if _, ok := rules["cycles"]; !ok {
		t.Fatalf("an explainer finding reports under the explainer's name as its rule: %v", rules)
	}
	if len(run.Results) != 5 {
		t.Fatalf("results = %d, want one per finding in every bucket", len(run.Results))
	}
	byBucket := map[string]int{}
	for _, r := range run.Results {
		byBucket[r.Properties.Bucket]++
		if r.PartialFingerprints["enola/v1"] == "" {
			t.Fatalf("result %q carries no enola/v1 fingerprint", r.Message.Text)
		}
		if run.Tool.Driver.Rules[r.RuleIndex].ID != r.RuleID {
			t.Fatalf("ruleIndex %d does not point at %q", r.RuleIndex, r.RuleID)
		}
		switch r.Properties.Bucket {
		case "failure":
			if r.Level != "error" || r.Properties.Policy != "fail" || r.Properties.SuggestedAction == "" {
				t.Fatalf("failure result = %+v", r)
			}
			loc := r.Locations[0].PhysicalLocation
			if loc.ArtifactLocation.URI != "app/models/user.rb" || loc.Region.StartLine != 12 || loc.Region.StartColumn != 5 || loc.Region.EndColumn != 11 {
				t.Fatalf("failure location = %+v", loc)
			}
		case "advisory":
			if r.Level != "warning" {
				t.Fatalf("advisory level = %q", r.Level)
			}
			if r.RuleID == "cycles" && len(r.Locations) != 0 {
				t.Fatalf("an unpositioned finding must carry no location: %+v", r.Locations)
			}
		case "suppressed":
			if r.Level != "note" || len(r.Suppressions) != 1 || !strings.Contains(r.Suppressions[0].Justification, "suppressed by dana on 2026-08-01: legacy admin") {
				t.Fatalf("suppressed result = %+v", r)
			}
		case "resolved":
			if r.Level != "none" || len(r.Locations) != 0 {
				t.Fatalf("a resolved finding carries no region and no level: %+v", r)
			}
		}
	}
	if byBucket["failure"] != 1 || byBucket["advisory"] != 2 || byBucket["suppressed"] != 1 || byBucket["resolved"] != 1 {
		t.Fatalf("buckets = %v", byBucket)
	}
}

func TestAnnotations_BuildkiteGroupsByFileAndCountsTheRest(t *testing.T) {
	out, err := formatVerdict().Annotations(HostBuildkite, "https://github.com/acme/shop/pull/7/files")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	want := []string{
		"### app/models/user.rb\n\n- [app/models/user.rb:12](https://github.com/acme/shop/pull/7/files#diff-",
		"R12) [failure] models-never-render: Constraint models-never-render violated: User#render -> render via calls Because: a model that knows the request cannot be used off it Action: Move the render into a presenter the controller owns\n",
		"### app/jobs/job.rb\n",
		"[suppressed] models-never-render: Constraint models-never-render violated: Admin#render -> render via calls Because: a model that knows the request cannot be used off it Action: Move the render into a presenter the controller owns (suppressed by dana on 2026-08-01: legacy admin, scheduled for removal)\n",
		"\n2 findings without a position stay in the summary.\n",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("annotations missing %q in:\n%s", w, got)
		}
	}
	if strings.Contains(got, "old.rb") {
		t.Fatalf("a resolved finding must not be placed:\n%s", got)
	}
	if strings.Index(got, "### app/models/user.rb") > strings.Index(got, "### app/jobs/job.rb") {
		t.Fatalf("failures come before advisories:\n%s", got)
	}
}

func TestAnnotations_GitHubWorkflowCommandsAndEscaping(t *testing.T) {
	v := formatVerdict()
	v.Failures[0].Title = "Constraint models-never-render violated: User#render -> render, 100% of the time\nreally"
	out, err := v.Annotations(HostGitHub, "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	line := strings.SplitN(got, "\n", 2)[0]
	if !strings.HasPrefix(line, "::error file=app/models/user.rb,line=12,endLine=12,col=5,endColumn=11,title=models-never-render::[failure] models-never-render: ") {
		t.Fatalf("first command = %q", line)
	}
	if !strings.Contains(line, "100%25 of the time really") || strings.Count(got, "\n") != 4 {
		t.Fatalf("data not escaped to one line per command: %q", line)
	}
	if !strings.Contains(got, "::warning file=app/jobs/job.rb,line=3,") {
		t.Fatalf("advisory not a warning:\n%s", got)
	}
	if !strings.Contains(got, "::notice file=app/models/admin.rb,line=7,") {
		t.Fatalf("suppressed not a notice:\n%s", got)
	}
	if !strings.HasSuffix(got, "::notice ::2 findings without a position stay in the summary.\n") {
		t.Fatalf("unplaced count missing:\n%s", got)
	}
}

func TestWrite_IsDeterministicAndTextAndJSONUnchanged(t *testing.T) {
	v := formatVerdict()
	for _, f := range []Format{FormatSARIF, FormatAnnotations} {
		a, err := v.Write(f, HostGitHub, "")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := v.Write(f, HostGitHub, "")
		if !bytes.Equal(a, b) {
			t.Fatalf("%s differs between two runs", f)
		}
	}
	text, _ := v.Write(FormatText, HostNone, "")
	if string(text) != v.Render() {
		t.Fatal("text format must be Render, byte for byte")
	}
	js, _ := v.Write(FormatJSON, HostNone, "")
	want, _ := v.JSON()
	if !bytes.Equal(js, want) {
		t.Fatal("json format must be JSON, byte for byte")
	}
	if _, err := v.Write(FormatAnnotations, HostNone, ""); err == nil {
		t.Fatal("annotations without a host must refuse, never print an empty document")
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Fatal("unknown format accepted")
	}
	if _, err := ParseHost("gitlab"); err == nil {
		t.Fatal("unknown host accepted")
	}
}
