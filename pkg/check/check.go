// Package check turns an architecture delta into a pass/fail verdict with an exit
// code, so the diff that `diff_snapshot` serves to an agent can also gate a commit
// or a CI job without an agent in the loop.
//
// It is deliberately a thin policy layer over internal/diff: Compute decides WHAT
// changed, Evaluate decides whether that is allowed to break a build. Evaluate is
// pure — same delta plus same policy always yields the same verdict — so a gate is
// as reproducible as the snapshot underneath it.
package check

import (
	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

// Status is the verdict. Its ExitCode is the process's contract with CI.
type Status string

const (
	// StatusClean — nothing the policy fails on. Advisory warnings may still print.
	StatusClean Status = "clean"
	// StatusRegression — the policy was violated.
	StatusRegression Status = "regression"
	// StatusUsageError — the caller's inputs were wrong in a way with a concrete
	// remedy (no baseline, an inverted snapshot pair).
	StatusUsageError Status = "usage_error"
	// StatusIncomparable — the two snapshots were not generated over equivalent
	// inputs, so the delta describes extraction differences rather than the change.
	// Distinct from StatusRegression on purpose: "I refuse to grade this" must never
	// be reported as "your change is bad".
	StatusIncomparable Status = "incomparable"
)

// ExitCode maps a status to the process exit code.
func (s Status) ExitCode() int {
	switch s {
	case StatusClean:
		return 0
	case StatusRegression:
		return 1
	case StatusUsageError:
		return 2
	case StatusIncomparable:
		return 3
	default:
		return 2
	}
}

// DefaultFailExplainers is empty, and that is the design: enola measures, reports, and
// fails nothing until you name what should fail.
//
// It used to hold "cycles". A dependency cycle is exactly measurable, which made it a
// tempting default — but exactly measurable is not the same as universally unwanted.
// Go's own standard library is written in a language whose packages cannot form one and
// whose authors treat the restriction as a design constraint rather than a virtue; a
// Rails app assembles most of its graph at runtime and its community does not read a
// cycle between two app/ directories as a defect at all. Shipping a default that breaks
// those builds meant enola arrived asserting a position on someone else's architecture
// before it had measured anything, and the first thing a Go or Rails team had to learn
// was which flag turns the opinion off.
//
// So the gate now enforces only what the caller states: --fail-on names the explainers,
// --max-spillover bounds the scope check, and a run with neither reports its findings and
// exits 0. Nothing is hidden by that — an unenforced run says so in its own output rather
// than printing a green line that could be mistaken for an all-clear (see Render).
//
// Deprecated: retained so a consumer that referenced this symbol still compiles. It is
// empty and no longer consulted; set Policy.FailExplainers to enforce anything.
var DefaultFailExplainers []string

// DefaultMinConfidence is the floor applied WITHIN the failing explainers.
//
// It still does real work: the cycles explainer emits both a true load-order cycle at
// confidence 1.0 and a "highly coupled module cluster" at 0.4 that its own description
// calls "an overall coupling-density signal, not a defect to break". The floor keeps the
// second out of the gate without needing a second allow-list.
const DefaultMinConfidence = 1.0

// Measurement is a count the policy can gate on that the DELTA ALONE DOES NOT CARRY —
// something the caller computed by running its own analysis over the two snapshots.
//
// It exists so that grading stays in one place while measuring stays with whoever can
// measure. A caller that owns an analyzer the engine does not ship can report what it
// found; it does not get to decide what that means for the verdict, because two surfaces
// deciding separately is how they come to disagree about the same change.
type Measurement struct {
	// Name is the stable key a Threshold refers to.
	Name string `json:"name"`
	// Label is the human phrasing used in the verdict line ("net-new dead-code
	// orphan(s)"), so a breach reads as a sentence rather than a variable name.
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Threshold turns a Measurement into a verdict contribution. A zero bound disables that
// severity, so {FailAt: 3} warns at nothing and fails at three.
//
// Both bounds live here rather than in the measuring code because the whole point is
// that one policy object describes what the gate enforces, whoever invoked it.
type Threshold struct {
	Measurement string `json:"measurement"`
	WarnAt      int    `json:"warn_at,omitempty"`
	FailAt      int    `json:"fail_at,omitempty"`
}

// Breach is a Measurement that met or exceeded one of its bounds.
type Breach struct {
	Measurement Measurement `json:"measurement"`
	// Fatal distinguishes a FailAt breach from a WarnAt one. A warning breach is
	// reported and does not change the exit code.
	Fatal bool `json:"fatal"`
}

// Policy decides which parts of a delta are allowed to fail a build.
type Policy struct {
	// FailExplainers are the explainer names whose new findings fail the gate.
	// EMPTY MEANS NOTHING FAILS: every new finding is reported as an advisory and the
	// verdict is clean. See DefaultFailExplainers for why the empty set is the default.
	FailExplainers []string `json:"fail_explainers"`
	// MinConfidence is the floor within FailExplainers. Zero means DefaultMinConfidence.
	MinConfidence float64 `json:"min_confidence"`
	// WarnOnly reports everything and still exits clean. Blocking incomparability and
	// usage errors are NOT suppressed by it — those are not judgements about the code,
	// they are statements that the gate could not run.
	WarnOnly bool `json:"warn_only"`
	// Thresholds gate on Measurements the caller supplies. EMPTY BY DEFAULT, which is
	// what keeps this additive: with no thresholds the verdict is byte-identical to
	// what it was before measurements existed, so a build that passes today still
	// passes. Anything that would newly fail a build has to be asked for.
	Thresholds []Threshold `json:"thresholds,omitempty"`
}

// thresholdFor returns the bound configured for a measurement, if any.
func (p Policy) thresholdFor(name string) (Threshold, bool) {
	for _, t := range p.Thresholds {
		if t.Measurement == name {
			return t, true
		}
	}
	return Threshold{}, false
}

// resolved fills in the defaults, so a Verdict records the policy that was actually
// ENFORCED rather than the one the caller happened to type. It matters most for the
// floor: a caller who set FailExplainers and left MinConfidence at zero is gating at
// 1.00, and JSON reporting `min_confidence: 0` would tell a consumer the opposite.
// FailExplainers is carried through untouched — an empty list is now a real answer
// ("nothing could fail"), not a stand-in for a default.
func (p Policy) resolved() Policy {
	return Policy{
		FailExplainers: p.failExplainers(),
		MinConfidence:  p.minConfidence(),
		WarnOnly:       p.WarnOnly,
		Thresholds:     p.Thresholds,
	}
}

func (p Policy) failExplainers() []string {
	return p.FailExplainers
}

// Enforcing reports whether this policy can fail anything at all. A run where it is
// false still grades and still reports; it just has no grounds to exit non-zero, and
// its output has to say so rather than print an unqualified PASS.
//
// Thresholds count: --max-spillover=0 with no --fail-on is a legitimate gate that
// enforces scope and no findings.
func (p Policy) Enforcing() bool {
	return len(p.FailExplainers) > 0 || len(p.Thresholds) > 0
}

func (p Policy) minConfidence() float64 {
	if p.MinConfidence == 0 {
		return DefaultMinConfidence
	}
	return p.MinConfidence
}

// fails reports whether one new finding violates the policy.
func (p Policy) fails(in facts.Insight) bool {
	// A descriptive finding is never a violation, whatever its confidence or explainer.
	// Without this, declaring a layer order for the first time fails the pull request
	// that declared it: the `layers` explainer emits an exact finding SAYING SO, and
	// --fail-on=layers would grade the description alongside the violations.
	if in.Informational {
		return false
	}
	if in.Confidence < p.minConfidence() {
		return false
	}
	// An unset Source cannot be matched against the allow-list. Treated as NOT failing:
	// findings from a snapshot written before Source was recorded would otherwise all
	// fail at once, which is a tooling artifact rather than a regression.
	for _, name := range p.failExplainers() {
		if in.Source == name {
			return true
		}
	}
	return false
}

// blockingKinds are the comparability warnings that make a delta meaningless rather
// than merely caveated: the numbers below them describe differences in how the two
// snapshots were produced, not the change under test.
// BlockingKinds returns the comparability warnings that make a delta untrustworthy —
// the ones that make the gate decline rather than grade.
//
// Exported so callers who need the classification without running the gate share it
// rather than re-deriving it: the session-start hook decides whether to refresh a
// baseline on exactly the grounds `enola check` would have refused to use it. Two
// copies of "which warnings are fatal" would drift, and the drift would be invisible.
func BlockingKinds(c diff.Comparability) []diff.WarningKind {
	var out []diff.WarningKind
	for _, k := range c.Kinds {
		if blockingKinds[k] {
			out = append(out, k)
		}
	}
	return out
}

var blockingKinds = map[diff.WarningKind]bool{
	diff.WarnDifferentRepo:   true,
	diff.WarnVersionMismatch: true,
	diff.WarnExtractorSet:    true,
	diff.WarnIgnoreGlobs:     true,
	// Fail closed on a caveat this package cannot categorize — see diff.AddWarning.
	diff.WarnUnclassified: true,
}

// advisoryKinds caveat a delta that is still worth grading. A stale baseline is the
// important member: internal/diff treats staleness as deliberately-not-a-refusal,
// because a long-lived baseline is a legitimate way to measure a multi-day refactor,
// and only the caller knows which they meant. The gate honours that — it says exactly
// why the baseline is stale and what it means for the delta, then grades anyway.
var advisoryKinds = map[diff.WarningKind]bool{
	diff.WarnStaleBaseline: true,
	diff.WarnPreReceipt:    true,
	// The explainer set differed. Deliberately advisory rather than blocking: unlike a
	// differing EXTRACTOR set, the fact delta is untouched, so the change is still
	// gradeable and only the findings from the explainers that differ are misattributed.
	// Registering it here is what makes "advisory" mean reported-and-graded rather than
	// silent — a kind in neither map is carried in ComparabilityWarnings but named in
	// no summary line.
	diff.WarnExplainerSet: true,
}

// Verdict is the graded delta.
type Verdict struct {
	Status Status `json:"status"`
	Policy Policy `json:"policy"`

	// Failures are the new findings that violated the policy.
	Failures []facts.Insight `json:"failures,omitempty"`
	// Advisories are new findings that did NOT violate it — reported so a clean exit
	// is still informative, never silent about a real structural change.
	Advisories []facts.Insight `json:"advisories,omitempty"`
	// Resolved are findings the change cleared. Worth printing: a gate that only ever
	// reports bad news trains people to read it as noise.
	Resolved []facts.Insight `json:"resolved,omitempty"`
	// Incidental are findings that moved with no structural cause in this change —
	// a drifting statistical threshold or a re-ranked top-N. Never graded.
	Incidental []facts.Insight `json:"incidental,omitempty"`
	// Descriptive are new findings that describe the graph rather than complain about
	// it (facts.Insight.Informational). Kept out of Advisories rather than merged into
	// them: the advisory note explains why a finding did NOT fail, and neither of its
	// reasons — under the floor, or outside --fail-on — is true of these.
	Descriptive []facts.Insight `json:"descriptive,omitempty"`

	// Measurements are every count the caller supplied, gated or not. Breaches are the
	// subset that met a threshold; a fatal one makes the status a regression exactly as
	// a failing finding does, so both surfaces reach the same verdict from the same
	// numbers.
	Measurements []Measurement `json:"measurements,omitempty"`
	Breaches     []Breach      `json:"breaches,omitempty"`

	// ComparabilityWarnings is every warning, verbatim and in full. Not split by
	// severity: diff.Comparability records kinds as a set rather than per-message, and
	// inventing an alignment to sort the prose would be a lie about which message
	// carried which kind. The kinds below name the verdict; these give the reasons.
	ComparabilityWarnings []string           `json:"comparability_warnings,omitempty"`
	BlockingKinds         []diff.WarningKind `json:"blocking_kinds,omitempty"`
	AdvisoryKinds         []diff.WarningKind `json:"advisory_kinds,omitempty"`

	// Structural tallies.
	EdgesAdded   int `json:"edges_added"`
	EdgesRemoved int `json:"edges_removed"`
	FactsAdded   int `json:"facts_added"`
	FactsRemoved int `json:"facts_removed"`
	// FactsChanged counts facts present in both snapshots whose own attributes moved
	// (a signature, an exported flag, a complexity metric) — a change that is neither an
	// addition nor a removal, and was previously reported as nothing at all.
	FactsChanged int `json:"facts_changed"`

	// AddedByKind / RemovedByKind break the fact tallies down per architectural kind, so
	// "+4 facts" reads as "one module and three symbols" without opening the full diff.
	AddedByKind   map[string]int `json:"added_by_kind,omitempty"`
	RemovedByKind map[string]int `json:"removed_by_kind,omitempty"`

	// EdgeKindsAdded / EdgeKindsRemoved break the edge tallies down per relation, which
	// is what makes the raw number readable: a large share of the edges any change adds
	// are `declares` — the mechanical one-per-new-symbol link to its module — and reading
	// "+56 coupling" without that split badly overstates what the change actually coupled.
	EdgeKindsAdded   map[string]int `json:"edge_kinds_added,omitempty"`
	EdgeKindsRemoved map[string]int `json:"edge_kinds_removed,omitempty"`

	// FindingsChanged are findings that survived under the same identity but whose
	// content moved — a god-class whose fan-in ticked up, a rollup whose count swung.
	// Carried because silence here reads as safety: the gate is the loop's own
	// collateral-damage instrument, and a finding that moved is something it saw.
	FindingsChanged []diff.InsightChange `json:"findings_changed,omitempty"`

	// Diff is the underlying delta, carried so --json emits one self-contained
	// document and so Render can delegate the detail view to internal/diff.
	Diff *diff.SnapshotDiff `json:"diff,omitempty"`
}

// ExitCode is the process exit code for this verdict.
func (v Verdict) ExitCode() int { return v.Status.ExitCode() }

// edgeKindCounts tallies edges by relation kind (the counterpart of diff.KindCounts).
func edgeKindCounts(edges []diff.Edge) map[string]int {
	m := make(map[string]int, len(edges))
	for _, e := range edges {
		m[e.Kind]++
	}
	return m
}

// Evaluate grades a delta against a policy.
//
// Precedence is blocking incomparability, then usage error, then regression. Blocking
// comes first because it is the more fundamental complaint: when the two snapshots were
// built over different inputs, "re-run generate_snapshot" (the remedy for an inverted
// pair) sends the caller down the wrong path. Nothing is hidden by the ordering — every
// warning is reported regardless of which one decided the status.
func Evaluate(d *diff.SnapshotDiff, p Policy, measurements ...Measurement) Verdict {
	v := Verdict{Policy: p.resolved(), Diff: d}
	if d == nil {
		v.Status = StatusUsageError
		return v
	}

	v.ComparabilityWarnings = d.Comparability.Warnings
	for _, k := range d.Comparability.Kinds {
		switch {
		case blockingKinds[k]:
			v.BlockingKinds = append(v.BlockingKinds, k)
		case advisoryKinds[k]:
			v.AdvisoryKinds = append(v.AdvisoryKinds, k)
		}
	}

	for _, in := range d.FindingsNew {
		switch {
		case in.Informational:
			v.Descriptive = append(v.Descriptive, in)
		case p.fails(in):
			v.Failures = append(v.Failures, in)
		default:
			v.Advisories = append(v.Advisories, in)
		}
	}
	v.Resolved = d.FindingsResolved
	v.Incidental = append(append([]facts.Insight{}, d.FindingsNewIncidental...), d.FindingsResolvedIncidental...)
	if len(v.Incidental) == 0 {
		v.Incidental = nil
	}

	v.EdgesAdded, v.EdgesRemoved = len(d.EdgesAdded), len(d.EdgesRemoved)
	v.FactsAdded, v.FactsRemoved = len(d.FactsAdded), len(d.FactsRemoved)
	v.FactsChanged = len(d.FactsChanged)
	v.AddedByKind = diff.KindCounts(d.FactsAdded)
	v.RemovedByKind = diff.KindCounts(d.FactsRemoved)
	v.EdgeKindsAdded = edgeKindCounts(d.EdgesAdded)
	v.EdgeKindsRemoved = edgeKindCounts(d.EdgesRemoved)
	v.FindingsChanged = d.FindingsChanged

	// Measurements are carried whether or not a threshold gates them: a caller that
	// measured something and got silence cannot tell "under the bound" from "never
	// looked", and that ambiguity is the same one the incidental bucket exists to avoid.
	v.Measurements = measurements
	fatalBreach := false
	for _, m := range measurements {
		t, ok := p.thresholdFor(m.Name)
		if !ok {
			continue
		}
		switch {
		case t.FailAt > 0 && m.Count >= t.FailAt:
			v.Breaches = append(v.Breaches, Breach{Measurement: m, Fatal: true})
			fatalBreach = true
		case t.WarnAt > 0 && m.Count >= t.WarnAt:
			v.Breaches = append(v.Breaches, Breach{Measurement: m})
		}
	}

	switch {
	case len(v.BlockingKinds) > 0:
		v.Status = StatusIncomparable
	case d.Comparability.HasKind(diff.WarnInvertedPair):
		v.Status = StatusUsageError
	case (len(v.Failures) > 0 || fatalBreach) && !p.WarnOnly:
		v.Status = StatusRegression
	default:
		v.Status = StatusClean
	}
	return v
}
