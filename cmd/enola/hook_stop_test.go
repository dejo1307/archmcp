package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/hookstate"
	"github.com/enola-labs/enola/pkg/check"
)

// declined builds a verdict in the state the gate reaches when it refuses to grade.
func declined(kinds ...diff.WarningKind) check.Verdict {
	d := &diff.SnapshotDiff{}
	for _, k := range kinds {
		d.AddWarningKind(k, "warning text for "+string(k))
	}
	return check.Evaluate(d, check.Policy{})
}

// writeBaselineMeta writes a baseline directory carrying an arbitrary meta, which the
// shared writeBaseline helper does not allow — the comparability rules being exercised
// here are all about fields it hard-codes.
func writeBaselineMeta(t *testing.T, meta facts.SnapshotMeta, auto bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "baseline")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "facts.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "snapshot.meta.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if auto {
		if err := os.WriteFile(filepath.Join(dir, autoPinMarker), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestStopOutcome_ClassifiesEveryPath(t *testing.T) {
	for _, tt := range []struct {
		name    string
		verdict check.Verdict
		ok      bool
		want    hookstate.Outcome
	}{
		{"graded, nothing wrong", check.Evaluate(&diff.SnapshotDiff{}, check.Policy{}), true, hookstate.OutcomeClean},
		{"could not grade", declined(diff.WarnVersionMismatch), true, hookstate.OutcomeDeclined},
		{"nothing to grade against", check.Verdict{}, false, hookstate.OutcomeUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := stopOutcome(tt.verdict, tt.ok); got != tt.want {
				t.Errorf("stopOutcome = %q, want %q", got, tt.want)
			}
		})
	}
}

// The reason an agent is shown must name the cause and the remedy. Anything vaguer
// leaves the reader unable to act, which is barely better than the silence this
// replaced.
func TestDeclineReason_NamesTheCauseAndIsEmptyOtherwise(t *testing.T) {
	v := declined(diff.WarnVersionMismatch)
	if v.Status != check.StatusIncomparable {
		t.Fatalf("expected an incomparable verdict, got %q", v.Status)
	}
	reason := v.DeclineReason()
	if reason == "" {
		t.Fatal("a declined verdict must produce a reason")
	}
	if !contains(reason, "version") {
		t.Errorf("reason should name the cause, got %q", reason)
	}

	clean := check.Evaluate(&diff.SnapshotDiff{}, check.Policy{})
	if clean.DeclineReason() != "" {
		t.Errorf("a graded verdict must have no decline reason, got %q", clean.DeclineReason())
	}
}

// DeclineKey drives the once-per-reason rule, so it has to distinguish DIFFERENT
// problems while treating the same problem as the same problem.
func TestDeclineKey_IdentifiesTheProblemNotTheProse(t *testing.T) {
	a := declined(diff.WarnVersionMismatch).DeclineKey()
	b := declined(diff.WarnVersionMismatch).DeclineKey()
	c := declined(diff.WarnDifferentRepo).DeclineKey()

	if a == "" {
		t.Fatal("a declined verdict needs a key")
	}
	if a != b {
		t.Errorf("the same decline must produce the same key: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("different declines must produce different keys, both %q", a)
	}
	// Order of the underlying set must not change the key.
	if declined(diff.WarnVersionMismatch, diff.WarnDifferentRepo).DeclineKey() !=
		declined(diff.WarnDifferentRepo, diff.WarnVersionMismatch).DeclineKey() {
		t.Error("the key must be order-independent")
	}
	if check.Evaluate(&diff.SnapshotDiff{}, check.Policy{}).DeclineKey() != "" {
		t.Error("a graded verdict has no key")
	}
}

// The whole point of the dedupe: say it once, say it again when it is a NEW problem,
// and say it again when a fixed problem comes back.
func TestShouldReport_OncePerReasonAndAgainAfterResolution(t *testing.T) {
	dir := t.TempDir()
	key := declined(diff.WarnVersionMismatch).DeclineKey()

	if !hookstate.ShouldReport(dir, hookstate.EventStop, key) {
		t.Fatal("a first decline must be reported")
	}
	hookstate.RecordFiredWithReason(dir, hookstate.EventStop, hookstate.OutcomeDeclined, key)

	if hookstate.ShouldReport(dir, hookstate.EventStop, key) {
		t.Error("the same decline repeating must stay quiet")
	}

	other := declined(diff.WarnDifferentRepo).DeclineKey()
	if !hookstate.ShouldReport(dir, hookstate.EventStop, other) {
		t.Error("a different decline is a new problem and must be reported")
	}

	// A successful grade clears the reason, so a recurrence is heard again — without
	// this, a problem fixed and then reintroduced would be suppressed forever.
	hookstate.RecordFiredWithReason(dir, hookstate.EventStop, hookstate.OutcomeClean, "")
	if !hookstate.ShouldReport(dir, hookstate.EventStop, key) {
		t.Error("a decline recurring after a clean grade must be reported again")
	}

	if hookstate.ShouldReport(dir, hookstate.EventStop, "") {
		t.Error("an empty reason is not a decline and must never be reported")
	}
}

// shouldAutoPin's new rule: refresh a baseline that can no longer be compared, but
// only one this hook created. A deliberate pin is the "before" of a refactor that may
// span days, and replacing it silently would destroy exactly what it was recording.
func TestShouldAutoPin_RefreshesUnusableAutoPinButNeverADeliberateOne(t *testing.T) {
	base := facts.SnapshotMeta{
		RepoPath: "/repo", EnolaVersion: "v1",
		Extractors: []string{"go"}, IgnoreGlobHash: "sha256:aaa",
	}
	current := base
	current.EnolaVersion = "v2" // blocking: different versions extract differently

	if !baselineIsUnusable(base, current) {
		t.Fatal("a version mismatch must count as unusable")
	}
	same := base
	if baselineIsUnusable(base, same) {
		t.Error("identical metadata must be usable")
	}

	// Auto-pinned and unusable → refreshed.
	autoDir := writeBaselineMeta(t, base, true)
	if !shouldAutoPin(autoDir, t.TempDir(), ".enola", &current) {
		t.Error("an auto-pinned baseline that can no longer be compared must be refreshed")
	}
	// Deliberate and unusable → left alone.
	deliberateDir := writeBaselineMeta(t, base, false)
	if shouldAutoPin(deliberateDir, t.TempDir(), ".enola", &current) {
		t.Error("a deliberately pinned baseline must never be replaced, even when unusable")
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
