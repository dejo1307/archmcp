package diff

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// A finding introduced by a change must be attributed to it even when NO symbol moved.
//
// The case that matters is a cycle closed between two modules that BOTH pre-date the
// change: the only new fact is the dependency, and the finding's evidence names the
// modules, not the dependency. Attribution works because touchedNames collects every
// added/removed/changed fact NAME *and both endpoints of every added/removed edge* —
// not only symbols.
//
// This is load-bearing beyond the diff itself. A downstream consumer that believed
// attribution was symbol-only would "rescue" such findings out of the incidental bucket
// to compensate, and that workaround re-admits exactly what the bucket exists to keep
// out: findings whose appearance the change did not cause.
func TestFindingAttributedThroughEdgeEndpoints(t *testing.T) {
	// Baseline: two modules, one importing the other. No cycle.
	base := &facts.Snapshot{
		Meta: facts.SnapshotMeta{RepoPath: "/r", GeneratedAt: "2026-08-01T10:00:00Z", EnolaVersion: "v1"},
		Facts: []facts.Fact{
			{Kind: facts.KindModule, Name: "pkgp", File: "pkgp"},
			{Kind: facts.KindModule, Name: "pkgq", File: "pkgq"},
			{
				Kind: facts.KindDependency, Name: "pkgq -> pkgp", File: "pkgq/q.go",
				Relations: []facts.Relation{{Kind: facts.RelImports, Target: "pkgp"}},
			},
		},
	}

	// Current: the same two modules, plus the import that closes the cycle. Neither
	// module is new; no symbol is added.
	current := &facts.Snapshot{
		Meta: facts.SnapshotMeta{RepoPath: "/r", GeneratedAt: "2026-08-01T10:00:01Z", EnolaVersion: "v1"},
		Facts: append(append([]facts.Fact{}, base.Facts...), facts.Fact{
			Kind: facts.KindDependency, Name: "pkgp -> pkgq", File: "pkgp/closer.go",
			Relations: []facts.Relation{{Kind: facts.RelImports, Target: "pkgq"}},
		}),
		Insights: []facts.Insight{{
			Source:     "cycles",
			Title:      "Cyclic dependency detected (2 modules)",
			Confidence: 1.0,
			Evidence: []facts.Evidence{
				{Fact: "pkgp", Detail: `module "pkgp" is part of the cycle`},
				{Fact: "pkgq", Detail: `module "pkgq" is part of the cycle`},
			},
		}},
	}

	d := Compute(base, current)

	if len(d.FindingsNew) != 1 {
		t.Fatalf("FindingsNew = %d, want 1 — the cycle was not attributed to the change "+
			"(incidental: %d)", len(d.FindingsNew), len(d.FindingsNewIncidental))
	}
	if len(d.FindingsNewIncidental) != 0 {
		t.Errorf("FindingsNewIncidental = %d, want 0 — a change-caused finding must not "+
			"be filed as incidental", len(d.FindingsNewIncidental))
	}
	if got := d.FindingsNew[0].Source; got != "cycles" {
		t.Errorf("attributed finding source = %q, want %q", got, "cycles")
	}
}

// The converse still holds: a finding citing nothing this change touched stays
// incidental. Without this, the test above could be satisfied by attributing
// everything, which would defeat the ratchet.
func TestUnrelatedFindingStaysIncidental(t *testing.T) {
	base := &facts.Snapshot{
		Meta: facts.SnapshotMeta{RepoPath: "/r", GeneratedAt: "2026-08-01T10:00:00Z", EnolaVersion: "v1"},
		Facts: []facts.Fact{
			{Kind: facts.KindModule, Name: "pkgp", File: "pkgp"},
			{Kind: facts.KindModule, Name: "elsewhere", File: "elsewhere"},
		},
	}
	current := &facts.Snapshot{
		Meta: facts.SnapshotMeta{RepoPath: "/r", GeneratedAt: "2026-08-01T10:00:01Z", EnolaVersion: "v1"},
		Facts: append(append([]facts.Fact{}, base.Facts...), facts.Fact{
			Kind: facts.KindSymbol, Name: "pkgp.New", File: "pkgp/new.go",
		}),
		// The finding cites a module the change never touched — a re-ranked top-N list,
		// not a consequence of this edit.
		Insights: []facts.Insight{{
			Source: "hotspots", Title: "Dependency hotspot", Confidence: 0.8,
			Evidence: []facts.Evidence{{Fact: "elsewhere", Detail: "high fan-in"}},
		}},
	}

	d := Compute(base, current)

	if len(d.FindingsNew) != 0 {
		t.Errorf("FindingsNew = %d, want 0 — a finding with no structural cause in this "+
			"change must not be reported as introduced by it", len(d.FindingsNew))
	}
	if len(d.FindingsNewIncidental) != 1 {
		t.Errorf("FindingsNewIncidental = %d, want 1", len(d.FindingsNewIncidental))
	}
}
