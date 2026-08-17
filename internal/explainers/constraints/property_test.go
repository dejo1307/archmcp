package constraints

// Property-style coverage over randomized small stores. Each seed builds a
// world from a fixed math/rand stream — deterministic, replayable by seed,
// no external libraries — compiles a REAL declaration through intent
// (validated, so the generator cannot drift from the vocabulary), and holds
// the invariants the explainer's contract states:
//
//   (a) determinism — the same store and declaration verdict identically
//       every time;
//   (b) fail-closed membership — a fact matching no component is never the
//       evidenced member of a membership verdict, and never the evidenced
//       target of a reverse-walk verdict;
//   (c) counterparty — a component naming an absent service silences every
//       rule that names it, leaving only the 0.4 advisory;
//   (d) witness oracle — every forbid_reach witness path is re-walked in the
//       store: every hop a measured edge of an allowed via kind, endpoints
//       the named members;
//   (e) confidence law — advisory and guidance findings never reach 1.0, and
//       ratchet/strict breaches never sit below it.

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

// propComponents maps component name -> root directory; fact names are
// "<root>.fN" and files "<root>/fN.x", so membership is decidable by name
// prefix in every oracle without reimplementing the resolver.
var propComponents = map[string]string{
	"compa": "roota",
	"compb": "rootb",
	"compc": "rootc",
	"compd": "rootd",
}

// ghostRoot holds facts deliberately outside every component.
const ghostRoot = "ghost"

var propViaPool = []string{facts.RelCalls, facts.RelDependsOn, facts.RelImports, facts.RelImplements}

// propWorld is what the generator knows about the world it built, for the
// oracles to check the verdicts against.
type propWorld struct {
	store       *facts.Store
	byName      map[string][]facts.Fact
	ruleForm    map[string]string // rule id -> form
	ruleVia     map[string]string // rule id -> declared via ("" = all)
	silencedIDs []string          // rules the counterparty must silence
	mode        string            // enforcement mode this seed ran under
	guideMode   string
}

func buildWorld(t *testing.T, seed int64) *propWorld {
	t.Helper()
	r := rand.New(rand.NewSource(seed))

	roots := []string{"roota", "rootb", "rootc", "rootd", ghostRoot}
	var names []string
	var worldFacts []facts.Fact
	for _, root := range roots {
		count := 3 + r.Intn(3)
		for i := 0; i < count; i++ {
			name := fmt.Sprintf("%s.f%d", root, i)
			kind := facts.KindSymbol
			props := map[string]any{"exported": r.Intn(2) == 0}
			if i == 0 {
				kind = facts.KindModule
				props = nil
			}
			worldFacts = append(worldFacts, facts.Fact{
				Kind: kind, Name: name, File: fmt.Sprintf("%s/f%d.x", root, i), Props: props,
			})
			names = append(names, name)
		}
	}
	for i := range worldFacts {
		edges := r.Intn(4)
		for e := 0; e < edges; e++ {
			target := names[r.Intn(len(names))]
			if r.Intn(10) < 3 {
				target = fmt.Sprintf("unresolved.u%d", r.Intn(50))
			}
			worldFacts[i].Relations = append(worldFacts[i].Relations, facts.Relation{
				Kind: propViaPool[r.Intn(len(propViaPool))], Target: target,
			})
		}
	}

	mode := []string{"", "advisory", "strict"}[r.Intn(3)]
	guideMode := []string{"", "advisory"}[r.Intn(2)]
	namePattern := []string{"rootc.*", "never*"}[r.Intn(2)]

	d := &intent.Declaration{
		Components: []intent.ConstraintComponent{
			{Name: "compa", Match: []string{"roota/**"}},
			{Name: "compb", Match: []string{"rootb/**"}},
			{Name: "compc", Match: []string{"rootc/**"}},
			{Name: "compd", Match: []string{"rootd/**"}},
			{Name: "remote", Service: "absent-svc"},
		},
		Rules: []intent.ConstraintRule{
			{ID: "prop-forbid", Forbid: "compa", To: "compb", Via: facts.RelCalls, Mode: mode, Because: "property"},
			{ID: "prop-reach", ForbidReach: "compa", To: "compc", Mode: mode, Because: "property"},
			{ID: "prop-allow", Allow: "compb", Only: []string{"compa"}, Via: facts.RelDependsOn, Mode: mode, Because: "property"},
			{ID: "prop-protect", Protect: "compc", Owners: []string{"compa"}, Via: facts.RelCalls, Mode: mode, Because: "property"},
			{ID: "prop-private", Private: "compa", Mode: mode, Because: "property"},
			{ID: "prop-forbidfact", ForbidFact: "compd", Mode: mode, Because: "property"},
			{ID: "prop-cap", Cap: "compb", MaxMembers: 2, Mode: mode, Because: "property"},
			{ID: "prop-edge", RequireEdge: "compa", To: "compb", Via: facts.RelCalls, Direction: "inbound", Mode: mode, Because: "property"},
			{ID: "prop-name", RequireName: "compc", Pattern: namePattern, Mode: mode, Because: "property"},
			{ID: "prop-protocol", Protocol: "compa", Steps: []string{"compb", "compc"}, Via: facts.RelCalls, Mode: mode, Because: "property"},
			{ID: "prop-absent", Forbid: "remote", To: "compa", Via: facts.RelCalls, Because: "property"},
			{ID: "prop-absent-target", ForbidReach: "compb", To: "remote", Because: "property"},
			{ID: "prop-guide", Guide: "compa", Message: "prior art exists", Mode: guideMode, Because: "property"},
		},
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("seed %d generated an invalid declaration: %v", seed, err)
	}

	store := facts.NewStore()
	store.Add(intent.CompileFacts(d)...)
	store.Add(worldFacts...)

	byName := map[string][]facts.Fact{}
	for _, f := range worldFacts {
		byName[f.Name] = append(byName[f.Name], f)
	}
	world := &propWorld{
		store:  store,
		byName: byName,
		ruleForm: map[string]string{
			"prop-forbid": "forbid", "prop-reach": "forbid_reach", "prop-allow": "allow",
			"prop-protect": "protect", "prop-private": "private", "prop-forbidfact": "forbid_fact",
			"prop-cap": "cap", "prop-name": "require_name", "prop-edge": "require_edge",
			"prop-protocol": "protocol",
			"prop-absent":   "forbid", "prop-absent-target": "forbid_reach",
		},
		ruleVia: map[string]string{
			"prop-forbid": facts.RelCalls, "prop-allow": facts.RelDependsOn,
			"prop-protect": facts.RelCalls, "prop-reach": "", "prop-private": "",
		},
		silencedIDs: []string{"prop-absent", "prop-absent-target"},
		mode:        mode,
		guideMode:   guideMode,
	}
	return world
}

// violationRule extracts the rule id from a breach title, or "" for
// non-breach insights (advisories about components, skips, guidance).
func violationRule(title string) string {
	idx := strings.Index(title, " violated: ")
	if idx < 0 {
		return ""
	}
	head := title[:idx]
	fields := strings.Fields(head)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

// memberOf reports whether a fact name lies inside a component, by the
// generator's own naming scheme.
func memberOf(name, component string) bool {
	root, ok := propComponents[component]
	return ok && strings.HasPrefix(name, root+".")
}

const propSeeds = 25

func TestProperty_EvaluatorDeterminism(t *testing.T) {
	for seed := int64(0); seed < propSeeds; seed++ {
		world := buildWorld(t, seed)
		first, err := New().Explain(context.Background(), world.store)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		second, err := New().Explain(context.Background(), world.store)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("seed %d: two evaluations of the same store differ", seed)
		}
	}
}

func TestProperty_FailClosedMembership(t *testing.T) {
	ruleTargetComp := map[string]string{"prop-protect": "compc", "prop-private": "compa"}
	ruleSourceComp := map[string]string{
		"prop-forbid": "compa", "prop-reach": "compa", "prop-allow": "compb",
		"prop-forbidfact": "compd", "prop-cap": "compb", "prop-name": "compc",
		"prop-edge": "compa", "prop-protocol": "compa",
	}
	for seed := int64(0); seed < propSeeds; seed++ {
		world := buildWorld(t, seed)
		insights, err := New().Explain(context.Background(), world.store)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for _, in := range insights {
			id := violationRule(in.Title)
			if id == "" {
				continue
			}
			if comp, ok := ruleSourceComp[id]; ok {
				for _, ev := range in.Evidence {
					if ev.Symbol != "" && !memberOf(ev.Symbol, comp) {
						t.Fatalf("seed %d: %s evidences %q, which is no member of %s: %s",
							seed, id, ev.Symbol, comp, in.Title)
					}
				}
			}
			if comp, ok := ruleTargetComp[id]; ok {
				for _, ev := range in.Evidence {
					if ev.Fact != "" && !memberOf(ev.Fact, comp) {
						t.Fatalf("seed %d: %s evidences target %q outside %s: %s",
							seed, id, ev.Fact, comp, in.Title)
					}
				}
			}
			// The private form's evidenced target must additionally be
			// non-exported on every fact bearing the name — the extractor's
			// own measurement, never assumed.
			if id == "prop-private" {
				for _, ev := range in.Evidence {
					for _, f := range world.byName[ev.Fact] {
						if exported, ok := f.Props["exported"].(bool); !ok || exported {
							t.Fatalf("seed %d: private verdict on %q, whose measurement is not exported:false", seed, ev.Fact)
						}
					}
				}
			}
		}
	}
}

func TestProperty_CounterpartySilencesAbsentServiceRules(t *testing.T) {
	for seed := int64(0); seed < propSeeds; seed++ {
		world := buildWorld(t, seed)
		insights, err := New().Explain(context.Background(), world.store)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		advisory := false
		for _, in := range insights {
			for _, id := range world.silencedIDs {
				if violationRule(in.Title) == id {
					t.Fatalf("seed %d: rule %s names an unasked component and still verdicted: %s", seed, id, in.Title)
				}
			}
			if strings.Contains(in.Title, "names service absent-svc not present") {
				advisory = true
				if in.Confidence != absentServiceConfidence {
					t.Fatalf("seed %d: absent-service advisory at %v, want %v", seed, in.Confidence, absentServiceConfidence)
				}
			}
		}
		if !advisory {
			t.Fatalf("seed %d: the counterparty silence left no visible trace", seed)
		}
	}
}

// witnessPath pulls the rendered path out of a forbid_reach description.
func witnessPath(description string) ([]string, bool) {
	const marker = "and the graph measures one: "
	start := strings.Index(description, marker)
	if start < 0 {
		return nil, false
	}
	rest := description[start+len(marker):]
	end := strings.Index(rest, ". The rule is declared")
	if end < 0 {
		return nil, false
	}
	return strings.Split(rest[:end], " -> "), true
}

func TestProperty_WitnessOracle(t *testing.T) {
	checked := 0
	for seed := int64(0); seed < propSeeds; seed++ {
		world := buildWorld(t, seed)
		insights, err := New().Explain(context.Background(), world.store)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for _, in := range insights {
			if violationRule(in.Title) != "prop-reach" {
				continue
			}
			path, ok := witnessPath(in.Description)
			if !ok {
				t.Fatalf("seed %d: reach verdict renders no witness path: %s", seed, in.Description)
			}
			if len(path) < 2 {
				t.Fatalf("seed %d: witness path %v has no hop", seed, path)
			}
			if !memberOf(path[0], "compa") {
				t.Fatalf("seed %d: witness starts at %q, no member of compa", seed, path[0])
			}
			if !memberOf(path[len(path)-1], "compc") {
				t.Fatalf("seed %d: witness ends at %q, no member of compc", seed, path[len(path)-1])
			}
			for hop := 0; hop+1 < len(path); hop++ {
				if !edgeExists(world, path[hop], path[hop+1]) {
					t.Fatalf("seed %d: witness hop %q -> %q is not a measured edge of an allowed via kind (path %v)",
						seed, path[hop], path[hop+1], path)
				}
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no forbid_reach violation surfaced across any seed; the oracle verified nothing")
	}
}

// edgeExists re-walks one hop in the raw store: some fact bearing the source
// name carries a relation of an allowed reach kind to the target.
func edgeExists(world *propWorld, source, target string) bool {
	allowed := map[string]bool{}
	for _, kind := range reachVias {
		allowed[kind] = true
	}
	for _, f := range world.byName[source] {
		for _, rel := range f.Relations {
			if allowed[rel.Kind] && rel.Target == target {
				return true
			}
		}
	}
	return false
}

// inboundCallsFrom re-walks the raw store: some fact under the source root
// carries a calls relation targeting the member by exact name.
func inboundCallsFrom(world *propWorld, sourceRoot, member string) bool {
	for name, ff := range world.byName {
		if !strings.HasPrefix(name, sourceRoot+".") {
			continue
		}
		for _, f := range ff {
			for _, rel := range f.Relations {
				if rel.Kind == facts.RelCalls && rel.Target == member {
					return true
				}
			}
		}
	}
	return false
}

func TestProperty_ExistentialOracle(t *testing.T) {
	checked := 0
	for seed := int64(0); seed < propSeeds; seed++ {
		world := buildWorld(t, seed)
		insights, err := New().Explain(context.Background(), world.store)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		skipped := false
		got := map[string]bool{}
		for _, in := range insights {
			if strings.HasPrefix(in.Title, "require_edge rule prop-edge skipped:") {
				skipped = true
			}
			if violationRule(in.Title) != "prop-edge" {
				continue
			}
			_, rest, found := strings.Cut(in.Title, " violated: ")
			if !found {
				t.Fatalf("seed %d: unparseable existential title: %s", seed, in.Title)
			}
			member, _, found := strings.Cut(rest, " has no inbound calls edge from compb")
			if !found {
				t.Fatalf("seed %d: existential witness off its declared shape: %s", seed, in.Title)
			}
			got[member] = true
		}
		if skipped {
			if len(got) != 0 {
				t.Fatalf("seed %d: a skipped existential rule must not also verdict", seed)
			}
			continue
		}
		for name := range world.byName {
			if !memberOf(name, "compa") {
				continue
			}
			orphan := !inboundCallsFrom(world, "rootb", name)
			switch {
			case orphan && !got[name]:
				t.Fatalf("seed %d: %q has no measured inbound calls edge from compb and was not verdicted", seed, name)
			case !orphan && got[name]:
				t.Fatalf("seed %d: %q has a measured inbound calls edge from compb and was verdicted anyway", seed, name)
			}
		}
		checked += len(got)
	}
	if checked == 0 {
		t.Fatal("no existential violation surfaced across any seed; the oracle verified nothing")
	}
}

func callsIntoRoot(world *propWorld, member, targetRoot string) bool {
	for _, f := range world.byName[member] {
		for _, rel := range f.Relations {
			if rel.Kind == facts.RelCalls && strings.HasPrefix(rel.Target, targetRoot+".") {
				return true
			}
		}
	}
	return false
}

func TestProperty_ProtocolOracle(t *testing.T) {
	checked := 0
	for seed := int64(0); seed < propSeeds; seed++ {
		world := buildWorld(t, seed)
		insights, err := New().Explain(context.Background(), world.store)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		skippedRule := false
		got := map[string]bool{}
		for _, in := range insights {
			if strings.HasPrefix(in.Title, "protocol rule prop-protocol skipped:") {
				skippedRule = true
			}
			if violationRule(in.Title) != "prop-protocol" {
				continue
			}
			_, rest, found := strings.Cut(in.Title, " violated: ")
			if !found {
				t.Fatalf("seed %d: unparseable protocol title: %s", seed, in.Title)
			}
			member, _, found := strings.Cut(rest, " calls compc without compb")
			if !found {
				t.Fatalf("seed %d: protocol witness off its declared shape: %s", seed, in.Title)
			}
			got[member] = true
		}
		if skippedRule {
			if len(got) != 0 {
				t.Fatalf("seed %d: a skipped protocol rule must not also verdict", seed)
			}
			continue
		}
		for name := range world.byName {
			if !memberOf(name, "compa") {
				continue
			}
			breach := callsIntoRoot(world, name, "rootc") && !callsIntoRoot(world, name, "rootb")
			switch {
			case breach && !got[name]:
				t.Fatalf("seed %d: %q calls the later step without the earlier one and was not verdicted", seed, name)
			case !breach && got[name]:
				t.Fatalf("seed %d: %q conforms or touches no later step and was verdicted anyway", seed, name)
			}
		}
		checked += len(got)
	}
	if checked == 0 {
		t.Fatal("no protocol violation surfaced across any seed; the oracle verified nothing")
	}
}

func TestProperty_ConfidenceLaw(t *testing.T) {
	sawBreach := false
	for seed := int64(0); seed < propSeeds; seed++ {
		world := buildWorld(t, seed)
		insights, err := New().Explain(context.Background(), world.store)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for _, in := range insights {
			isBreach := violationRule(in.Title) != ""
			switch {
			case strings.HasPrefix(in.Title, "Advisory constraint"):
				if in.Confidence >= 1.0 {
					t.Fatalf("seed %d: advisory breach at %v: %s", seed, in.Confidence, in.Title)
				}
			case strings.HasPrefix(in.Title, "Guidance for"):
				if in.Confidence >= 1.0 {
					t.Fatalf("seed %d: guidance at %v can fail a gate: %s", seed, in.Confidence, in.Title)
				}
				if world.guideMode != "advisory" {
					t.Fatalf("seed %d: a notify-mode guidance rule emitted a finding: %s", seed, in.Title)
				}
			case isBreach:
				sawBreach = true
				if in.Confidence != 1.0 {
					t.Fatalf("seed %d: a declared law's breach at %v, want 1.0: %s", seed, in.Confidence, in.Title)
				}
			default:
				if in.Confidence >= 1.0 {
					t.Fatalf("seed %d: a non-breach insight at %v reads as law: %s", seed, in.Confidence, in.Title)
				}
			}
		}
	}
	if !sawBreach {
		t.Fatal("no ratchet or strict breach surfaced across any seed; the law half was never exercised")
	}
}
