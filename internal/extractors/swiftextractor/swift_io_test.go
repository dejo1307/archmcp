package swiftextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func hasBoolProp(f facts.Fact, key string) bool {
	b, _ := f.Props[key].(bool)
	return b
}

func TestSwIO_DirectNetworkPrimitiveSetsIODirect(t *testing.T) {
	cases := map[string]string{
		"urlsession":   "func run() { URLSession.shared.dataTask(with: req) { _,_,_ in } }",
		"datafor":      "func run() async { _ = try? await URLSession.shared.data(for: req) }",
		"alamofire":    "func run() { AF.request(url).responseData { _ in } }",
		"contentsOf":   "func run() { _ = try? Data(contentsOf: url) }",
		"downloadTask": "func run() { session.downloadTask(with: url).resume() }",
	}
	for name, src := range cases {
		ff := extractAST(t, src, false)
		f, ok := findFact(ff, "pkg.run")
		if !ok {
			t.Fatalf("%s: missing pkg.run", name)
		}
		if !hasBoolProp(f, "io_direct") {
			t.Errorf("%s: expected io_direct=true for %q", name, src)
		}
	}
}

func TestSwIO_InMemoryCallNoIODirect(t *testing.T) {
	ff := extractAST(t, "func run() { badge.updateCenter(); dict.updateValue(1, forKey: k) }", false)
	f, _ := findFact(ff, "pkg.run")
	if hasBoolProp(f, "io_direct") {
		t.Errorf("in-memory update* calls must not set io_direct")
	}
}

func symFact(name, target string, ioDirect bool) facts.Fact {
	f := facts.Fact{
		Kind:  facts.KindSymbol,
		Name:  name,
		Props: map[string]any{"symbol_kind": facts.SymbolMethod},
	}
	if ioDirect {
		f.Props["io_direct"] = true
	}
	if target != "" {
		f.Relations = []facts.Relation{{Kind: facts.RelCalls, Target: target}}
	}
	return f
}

func TestComputePerformsIO_TransitiveThroughAmbiguousEdge(t *testing.T) {
	// d.VM.foo calls a BARE ambiguous "bar" (two candidates); one candidate does I/O.
	// The closure must cross that edge via methodIndex and mark d.VM.foo performs_io.
	ff := []facts.Fact{
		symFact("d.VM.foo", "bar", false),
		symFact("d.VM.bar", "", false), // in-memory sibling
		symFact("d.API.bar", "", true), // does I/O
	}
	methodIndex := map[string][]string{"bar": {"d.VM.bar", "d.API.bar"}}
	computePerformsIO(ff, methodIndex, nil)

	if !hasBoolProp(ff[0], "performs_io") {
		t.Errorf("d.VM.foo should be performs_io via the ambiguous bar -> d.API.bar edge")
	}
	if !hasBoolProp(ff[2], "performs_io") {
		t.Errorf("d.API.bar (io_direct) should be performs_io")
	}
	if hasBoolProp(ff[1], "performs_io") {
		t.Errorf("d.VM.bar (in-memory) should NOT be performs_io")
	}
}

func TestComputePerformsIO_MultiHopAndCycleSafe(t *testing.T) {
	// Multi-hop A -> B -> C(io): all three reach I/O. Separately, a no-I/O cycle
	// X <-> Y must terminate and stay non-I/O.
	ff := []facts.Fact{
		symFact("p.A", "p.B", false),
		symFact("p.B", "p.C", false),
		symFact("p.C", "", true),
		symFact("p.X", "p.Y", false),
		symFact("p.Y", "p.X", false),
	}
	computePerformsIO(ff, nil, nil)

	for _, i := range []int{0, 1, 2} {
		if !hasBoolProp(ff[i], "performs_io") {
			t.Errorf("%s should be performs_io (multi-hop to I/O)", ff[i].Name)
		}
	}
	for _, i := range []int{3, 4} {
		if hasBoolProp(ff[i], "performs_io") {
			t.Errorf("%s is in a no-I/O cycle and must not be performs_io", ff[i].Name)
		}
	}
}

func TestComputePerformsIO_FanoutCapBoundsPropagation(t *testing.T) {
	// A bare target with more candidates than the cap is NOT expanded, so an I/O
	// method hidden behind a very common name does not over-propagate.
	ff := []facts.Fact{symFact("p.caller", "common", false)}
	cands := make([]string, 0, ioFanoutCap+2)
	for i := 0; i < ioFanoutCap+1; i++ {
		name := "p.T" + string(rune('a'+i)) + ".common"
		ff = append(ff, symFact(name, "", i == 0)) // first candidate does I/O
		cands = append(cands, name)
	}
	computePerformsIO(ff, map[string][]string{"common": cands}, nil)
	if hasBoolProp(ff[0], "performs_io") {
		t.Errorf("caller must not get performs_io: the ambiguous 'common' exceeds the fan-out cap")
	}
}
