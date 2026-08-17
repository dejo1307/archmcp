package providers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// writeProvider writes an executable shell script that answers --version with
// the given semver and otherwise prints the given JSONL body — the smallest
// thing that honors the provider contract.
func writeProvider(t *testing.T, version, jsonl string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-provider")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do if [ \"$a\" = \"--version\" ]; then echo " + version + "; exit 0; fi; done\n" +
		"cat <<'EOF'\n" + jsonl + "EOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const validCallFact = `{"kind":"dependency","name":"prism-call: A#m -> B#n","file":"app/a.rb","props":{"resolution_level":"constant-receiver"},"relations":[{"kind":"calls","target":"B#n"}]}`

func TestRun_StampsSortsAndReportsCensus(t *testing.T) {
	// Deliberately out of order: the seam's sort, not the provider's manners,
	// is what determinism rests on.
	script := writeProvider(t, "1.2.3",
		`{"kind":"symbol","name":"zz","file":"z.rb","props":{"resolution_level":"name-only"}}`+"\n"+
			validCallFact+"\n")
	ff, records := Run(context.Background(), []Provider{{Name: "fake", Command: []string{script}}}, t.TempDir(), nil)
	if len(ff) != 2 {
		t.Fatalf("facts = %+v, want 2", ff)
	}
	if ff[0].Kind != "dependency" || ff[1].Kind != "symbol" {
		t.Errorf("facts must be sorted before merge, got %s then %s", ff[0].Name, ff[1].Name)
	}
	for _, f := range ff {
		if f.Props[PropProvider] != "fake" || f.Props[PropProviderVersion] != "1.2.3" {
			t.Errorf("fact %q not stamped with provenance: %+v", f.Name, f.Props)
		}
	}
	if len(records) != 1 || records[0].Name != "fake" || records[0].Version != "1.2.3" ||
		records[0].FactCount != 2 || records[0].Skipped {
		t.Errorf("census = %+v", records)
	}
}

func TestRun_MissingCommandIsANamedSkipNeverAnError(t *testing.T) {
	ff, records := Run(context.Background(),
		[]Provider{{Name: "ghost", Command: []string{"/no/such/enola-provider"}}}, t.TempDir(), nil)
	if len(ff) != 0 {
		t.Fatalf("a skipped provider must contribute nothing, got %+v", ff)
	}
	if len(records) != 1 || !records[0].Skipped || !strings.Contains(records[0].Reason, "not found") {
		t.Fatalf("census = %+v, want a named command-not-found skip", records)
	}
}

func TestRun_VersionMismatchIsASkip(t *testing.T) {
	script := writeProvider(t, "1.2.3", validCallFact+"\n")
	ff, records := Run(context.Background(),
		[]Provider{{Name: "fake", Command: []string{script}, ExpectedVersion: "2.0.0"}}, t.TempDir(), nil)
	if len(ff) != 0 || !records[0].Skipped ||
		!strings.Contains(records[0].Reason, "reported 1.2.3, expected 2.0.0") {
		t.Fatalf("facts = %+v, census = %+v", ff, records)
	}
	if records[0].Version != "1.2.3" {
		t.Errorf("the census must still say what the tool reported, got %+v", records[0])
	}
}

func TestRun_InvalidLineRejectsTheWholeOutput(t *testing.T) {
	for name, badLine := range map[string]string{
		"unknown kind":             `{"kind":"wibble","name":"x","props":{"resolution_level":"name-only"}}`,
		"unknown relation":         `{"kind":"symbol","name":"x","props":{"resolution_level":"name-only"},"relations":[{"kind":"summons","target":"y"}]}`,
		"unknown field":            `{"kind":"symbol","name":"x","confidence":1,"props":{"resolution_level":"name-only"}}`,
		"missing resolution level": `{"kind":"symbol","name":"x"}`,
		"unknown resolution level": `{"kind":"symbol","name":"x","props":{"resolution_level":"vibes"}}`,
		"channelless runtime fact": `{"kind":"route","name":"runtime-route: GET /x","props":{"resolution_level":"runtime-observed"}}`,
		"claimed provenance":       `{"kind":"symbol","name":"x","props":{"resolution_level":"name-only","provider":"me"}}`,
		"engine-assigned repo":     `{"kind":"symbol","name":"x","repo":"r","props":{"resolution_level":"name-only"}}`,
		"targetless relation":      `{"kind":"symbol","name":"x","props":{"resolution_level":"name-only"},"relations":[{"kind":"calls","target":""}]}`,
		"not json":                 `plainly not a fact`,
	} {
		t.Run(name, func(t *testing.T) {
			script := writeProvider(t, "1.2.3", validCallFact+"\n"+badLine+"\n")
			ff, records := Run(context.Background(),
				[]Provider{{Name: "fake", Command: []string{script}}}, t.TempDir(), nil)
			if len(ff) != 0 {
				t.Fatalf("one invalid line must reject the whole output, got %+v", ff)
			}
			if !records[0].Skipped || !strings.Contains(records[0].Reason, "line 2") {
				t.Fatalf("census = %+v, want a skip naming line 2", records)
			}
		})
	}
}

func TestRun_ExtractorIdentityIsNeverOverwritten(t *testing.T) {
	script := writeProvider(t, "1.2.3",
		validCallFact+"\n"+
			`{"kind":"symbol","name":"Taken","file":"a.rb","props":{"resolution_level":"name-only"}}`+"\n")
	taken := func(kind, name string) bool { return kind == facts.KindSymbol && name == "Taken" }
	ff, records := Run(context.Background(),
		[]Provider{{Name: "fake", Command: []string{script}}}, t.TempDir(), taken)
	if len(ff) != 1 || ff[0].Name != "prism-call: A#m -> B#n" {
		t.Fatalf("facts = %+v, want only the non-colliding one", ff)
	}
	if records[0].FactCount != 1 {
		t.Errorf("census must count merged facts, not emitted ones: %+v", records[0])
	}
}

func TestRun_ProvidersRunInNameOrder(t *testing.T) {
	beta := writeProvider(t, "1.0.0", `{"kind":"symbol","name":"from-beta","props":{"resolution_level":"name-only"}}`+"\n")
	alpha := writeProvider(t, "1.0.0", `{"kind":"symbol","name":"from-alpha","props":{"resolution_level":"name-only"}}`+"\n")
	ff, records := Run(context.Background(), []Provider{
		{Name: "beta", Command: []string{beta}},
		{Name: "alpha", Command: []string{alpha}},
	}, t.TempDir(), nil)
	if len(records) != 2 || records[0].Name != "alpha" || records[1].Name != "beta" {
		t.Fatalf("census order = %+v, want name order regardless of config order", records)
	}
	if len(ff) != 2 || ff[0].Name != "from-alpha" || ff[1].Name != "from-beta" {
		t.Fatalf("merge order = %+v, want alpha's facts first", ff)
	}
}

func TestValidate_NamesAndCommandsAreRequired(t *testing.T) {
	for name, bad := range map[string][]Provider{
		"missing name":    {{Command: []string{"x"}}},
		"missing command": {{Name: "p"}},
		"duplicate name":  {{Name: "p", Command: []string{"x"}}, {Name: "p", Command: []string{"y"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Validate(bad); err == nil {
				t.Fatal("an ill-formed provider config must be a validation error")
			}
		})
	}
	if err := Validate([]Provider{{Name: "p", Command: []string{"x", "--flag"}, ExpectedVersion: "1.0.0"}}); err != nil {
		t.Fatalf("a well-formed provider config must validate, got %v", err)
	}
}
