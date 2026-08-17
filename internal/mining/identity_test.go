package mining

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func identitySet(report *Report) map[string]bool {
	set := map[string]bool{}
	for _, c := range report.Candidates {
		set[c.Identity] = true
	}
	return set
}

func TestCandidateIdentityIsStableAcrossInsertionOrders(t *testing.T) {
	base := fullWorld()
	want := identitySet(Mine(storeOf(base), DefaultConfig()))
	if len(want) == 0 {
		t.Fatal("no identities mined from the full world")
	}
	for seed := int64(1); seed <= 3; seed++ {
		shuffled := append([]facts.Fact{}, base...)
		rand.New(rand.NewSource(seed)).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := identitySet(Mine(storeOf(shuffled), DefaultConfig()))
		if len(got) != len(want) {
			t.Fatalf("seed %d: %d identities, want %d", seed, len(got), len(want))
		}
		for id := range want {
			if !got[id] {
				t.Errorf("seed %d: identity %q vanished under reordering", seed, id)
			}
		}
	}
}

func TestCandidateIdentitySurvivesRegularityShift(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinConfidence = 0.7
	world := append(namingWorld(), methodWorld()...)
	before := Mine(storeOf(world), cfg)
	eroded := append([]facts.Fact{}, world...)
	eroded[0].Name = "AlphaPresenter"
	eroded[1].Name = "BravoPresenter"
	after := Mine(storeOf(eroded), cfg)

	beforeCandidate := findCandidate(t, before, FamilyNaming, "*Serializer")
	afterCandidate := findCandidate(t, after, FamilyNaming, "*Serializer")
	if beforeCandidate.Identity != afterCandidate.Identity {
		t.Fatalf("identity moved with the numbers: %q vs %q", beforeCandidate.Identity, afterCandidate.Identity)
	}
	if beforeCandidate.Numerator != 12 || afterCandidate.Numerator != 10 {
		t.Errorf("regularity = %d then %d, want 12 then 10", beforeCandidate.Numerator, afterCandidate.Numerator)
	}
}

func TestCandidateIdentitiesAreUniqueWithinAReport(t *testing.T) {
	report := Mine(storeOf(fullWorld()), DefaultConfig())
	seen := map[string]string{}
	for _, c := range report.Candidates {
		if c.Identity == "" {
			t.Fatalf("candidate without identity: %s", c.Statement)
		}
		if prior, dup := seen[c.Identity]; dup {
			t.Errorf("identity %q names both %q and %q", c.Identity, prior, c.Statement)
		}
		seen[c.Identity] = c.Statement
	}
}

func TestIdentityKeyIsUnambiguousUnderSeparatorCharacters(t *testing.T) {
	if identityKey("a|b") == identityKey("a", "b") {
		t.Error("a part containing the separator collides with two parts")
	}
	if identityKey(`a\`, "b") == identityKey(`a\|b`) {
		t.Error("escape handling is ambiguous")
	}
}

func TestJSONLArtifactCarriesIdentity(t *testing.T) {
	report := Mine(storeOf(fullWorld()), DefaultConfig())
	var jsonl bytes.Buffer
	if err := report.WriteJSONL(&jsonl); err != nil {
		t.Fatal(err)
	}
	candidates := 0
	for _, line := range strings.Split(strings.TrimSpace(jsonl.String()), "\n") {
		var parsed struct {
			Type     string `json:"type"`
			Identity string `json:"identity"`
		}
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Type != "candidate" {
			continue
		}
		candidates++
		if parsed.Identity == "" {
			t.Fatalf("candidate line without identity: %s", line)
		}
	}
	if candidates == 0 {
		t.Fatal("no candidate lines in the artifact")
	}
}
