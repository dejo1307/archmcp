package providers

import (
	"strings"
	"testing"
)

// FuzzParseFacts drives the provider JSONL validator with arbitrary bytes.
// The contract under fuzz is strict validation's own: any input either yields
// facts that individually satisfy every rule the validator enforces, or a
// named error and no facts at all — never a panic, and never a fact that
// slipped a rule, because an accepted provider fact enters the graph as if it
// were measured.
func FuzzParseFacts(f *testing.F) {
	for _, seed := range []string{
		`{"kind":"dependency","name":"prism-call: A#m -> B#n","file":"app/a.rb","props":{"resolution_level":"constant-receiver"},"relations":[{"kind":"calls","target":"B#n"}]}`,
		`{"kind":"symbol","name":"zz","file":"z.rb","props":{"resolution_level":"name-only"}}`,
		`{"kind":"wibble","name":"x","props":{"resolution_level":"name-only"}}`,
		`{"kind":"symbol","name":"x","props":{"resolution_level":"name-only"},"relations":[{"kind":"summons","target":"y"}]}`,
		`{"kind":"symbol","name":"x","repo":"r","props":{"resolution_level":"name-only"}}`,
		`{"kind":"symbol","name":"x","props":{"resolution_level":"name-only","provider":"me"}}`,
		"plainly not a fact",
		"",
		"\n\n\n",
		`{"kind":"symbol","name":"x","props":null}`,
		`{"kind":"route","name":"runtime-route: GET /health","props":{"resolution_level":"runtime-observed","observed_via":"rails-boot","method":"GET","path":"/health"}}`,
		`{"kind":"route","name":"runtime-route: GET /x","props":{"resolution_level":"runtime-observed"}}`,
		`{"kind":"symbol","name":"rbs-signature: Foo#bar","file":"sig/foo.rbs","props":{"resolution_level":"declared","declared_in":"sig/foo.rbs","receiver":"Foo","method":"bar","singleton":false,"signature":"() -> void"},"relations":[{"kind":"has_method","target":"Foo#bar"}]}`,
		`{"kind":"symbol","name":"rbs-signature: Foo#bar","props":{"resolution_level":"declared"}}`,
		`{"kind":"symbol","name":"x","props":{"resolution_level":"vibes"}}`,
		`{"kind":"symbol","name":"x","props":{"resolution_level":"name-only"},"relations":[null]}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		accepted, err := parseFacts(data)
		if err != nil {
			if accepted != nil {
				t.Fatalf("a rejected output must yield no facts, got %d beside %v", len(accepted), err)
			}
			return
		}
		for _, fact := range accepted {
			if !allowedFactKinds[fact.Kind] {
				t.Fatalf("accepted fact carries kind %q outside the vocabulary", fact.Kind)
			}
			if fact.Name == "" {
				t.Fatal("accepted fact has no name")
			}
			if fact.Repo != "" {
				t.Fatalf("accepted fact claims the engine-assigned repo %q", fact.Repo)
			}
			level, _ := fact.Props[PropResolutionLevel].(string)
			if !allowedResolutionLevels[level] {
				t.Fatalf("accepted fact %q carries resolution level %q outside the vocabulary", fact.Name, level)
			}
			if via, _ := fact.Props[PropObservedVia].(string); level == LevelRuntimeObserved && via == "" {
				t.Fatalf("accepted runtime fact %q names no observation channel", fact.Name)
			}
			if in, _ := fact.Props[PropDeclaredIn].(string); level == LevelDeclared && in == "" {
				t.Fatalf("accepted declared fact %q names no signature file", fact.Name)
			}
			if _, claimed := fact.Props[PropProvider]; claimed {
				t.Fatalf("accepted fact %q claims the seam-stamped provider prop", fact.Name)
			}
			for _, rel := range fact.Relations {
				if !allowedRelationKinds[rel.Kind] || rel.Target == "" {
					t.Fatalf("accepted fact %q carries relation %+v outside the vocabulary", fact.Name, rel)
				}
			}
		}
		if strings.TrimSpace(string(data)) == "" && len(accepted) != 0 {
			t.Fatal("blank output must yield no facts")
		}
	})
}
