// Package hookstate records when enola's agent hooks last ran.
//
// It exists because of a defect that no test in this repository could have caught.
// `enola install --hooks` wrote the Stop hook in a JSON shape the agent ignored, so
// the hooks never fired — and every cheaper check passed while it was broken: the
// installer wrote the file it meant to, the hook binary produced the right verdict
// when invoked by hand, and a unit test asserted the shape against the same belief
// that produced it. See DEFECTS_FOUND.md.
//
// The shape of that config is a contract owned by the agent, which ships on its own
// schedule and can change after enola is released. So it is not testable here in any
// durable way: the only defence that survives the contract moving is noticing, on the
// machine where it matters, that the hooks have stopped firing.
//
// Hence a heartbeat. Every hook invocation records that it ran — INCLUDING the runs
// where it deliberately says nothing, which is the overwhelming majority and the whole
// point. Silence was the defect's entire signature; a hook that has never fired is now
// distinguishable from one that fires and finds nothing to report.
package hookstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// FileName is the heartbeat file, written inside the engine's output directory.
//
// It is deliberately NOT one of engine.snapshotArtifactFiles, so `baseline pin` does
// not copy it into baseline/ and a heartbeat can never be mistaken for snapshot state.
// The output directory is excluded from indexing, so this file contributes no facts
// and cannot affect a snapshot ID.
const FileName = "hooks.json"

// Outcome is what a hook run concluded. Recorded because "fired 40 times and always
// declined to grade" and "fired 40 times and found nothing wrong" look identical from
// a timestamp alone, and only one of them is a problem.
type Outcome string

const (
	// OutcomeReported: a regression was found and handed back to the agent.
	OutcomeReported Outcome = "reported"
	// OutcomeClean: graded successfully, nothing to report. The normal case.
	OutcomeClean Outcome = "clean"
	// OutcomeDeclined: the baseline was not comparable, so no verdict was possible.
	// The hook stays silent in-session; this is where that silence becomes visible.
	OutcomeDeclined Outcome = "declined"
	// OutcomeUnavailable: nothing to grade against — usually no baseline pinned yet.
	OutcomeUnavailable Outcome = "unavailable"
	// OutcomePinned: the session-start hook froze a baseline.
	OutcomePinned Outcome = "pinned"
	// OutcomeSkipped: the session-start hook deliberately did no work — a deliberate
	// baseline it must not replace, an unchanged tree, or another session holding the lock.
	OutcomeSkipped Outcome = "skipped"
)

// Event names a hook. These match the `enola hook <event>` subcommands.
type Event string

const (
	EventStop         Event = "stop"
	EventSessionStart Event = "session-start"
)

// Record is one event's history. Count is cumulative; the timestamps bound it.
type Record struct {
	FirstFired  time.Time `json:"first_fired"`
	LastFired   time.Time `json:"last_fired"`
	Count       int       `json:"count"`
	LastOutcome Outcome   `json:"last_outcome,omitempty"`

	// LastReason identifies WHICH decline was last reported, so a repeat can be
	// recognised and left unsaid. Empty whenever the last run did not decline —
	// including a run that graded successfully, which is what makes a recurrence
	// speak again instead of being suppressed forever by a problem that was fixed
	// in between.
	//
	// Absent from older heartbeat files, where it reads as the zero value: an
	// upgrade therefore reports the current decline once, which is the right
	// behaviour rather than a migration.
	LastReason string `json:"last_reason,omitempty"`
}

// State is the whole file.
type State struct {
	// InstalledAt is when `install --hooks` last configured this repository. It is what
	// makes "never fired" meaningful: without it, an empty file cannot be told apart
	// from hooks that were never installed in the first place.
	InstalledAt time.Time         `json:"installed_at,omitempty"`
	Events      map[Event]*Record `json:"events,omitempty"`
	// HookCommand records which binary the installed hooks invoke, so a heartbeat that
	// stopped can be attributed to a moved or replaced enola rather than to the agent.
	HookCommand string `json:"hook_command,omitempty"`
}

// Fired reports whether the event has ever run.
func (s State) Fired(e Event) bool {
	r, ok := s.Events[e]
	return ok && r.Count > 0
}

// Get returns the record for an event, or nil.
func (s State) Get(e Event) *Record { return s.Events[e] }

// Path returns the heartbeat file's location for an output directory.
func Path(outDir string) string { return filepath.Join(outDir, FileName) }

// Load reads the heartbeat. A missing or unreadable file is not an error: it means
// "nothing recorded", which is a legitimate state and the one a fresh repository is in.
func Load(outDir string) State {
	var s State
	data, err := os.ReadFile(Path(outDir))
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return State{} // a corrupt heartbeat is worth less than no heartbeat
	}
	return s
}

// RecordFired stamps an event. Best-effort and silent by contract: this is called from
// a hook, and a hook that cannot fail loudly must not try. Every error path returns
// without disturbing the caller.
func RecordFired(outDir string, e Event, o Outcome) { RecordFiredWithReason(outDir, e, o, "") }

// RecordFiredWithReason is RecordFired carrying the decline identity from
// check.Verdict.DeclineKey(). Pass "" for every outcome that is not a decline — that
// is what clears a previously reported reason, so the same problem returning after
// being fixed is reported again rather than silently suppressed.
func RecordFiredWithReason(outDir string, e Event, o Outcome, reason string) {
	mutate(outDir, func(s *State) {
		if s.Events == nil {
			s.Events = map[Event]*Record{}
		}
		r := s.Events[e]
		if r == nil {
			r = &Record{FirstFired: now()}
			s.Events[e] = r
		}
		r.LastFired = now()
		r.Count++
		r.LastOutcome = o
		r.LastReason = reason
	})
}

// ShouldReport reports whether a decline with this identity is worth saying out loud.
//
// True when it differs from what was last recorded — a first occurrence, a different
// problem, or the same problem returning after a successful grade cleared it. False
// for an unchanged repeat, because a hook that says the same thing at the end of every
// session is one people uninstall, and the standing state is visible in `enola doctor`
// either way.
func ShouldReport(outDir string, e Event, reason string) bool {
	if reason == "" {
		return false
	}
	r := Load(outDir).Get(e)
	return r == nil || r.LastReason != reason
}

// RecordInstalled stamps the install time and the hook command, so a later report can
// say "installed on X, never fired since".
func RecordInstalled(outDir, hookCommand string) {
	mutate(outDir, func(s *State) {
		s.InstalledAt = now()
		s.HookCommand = hookCommand
	})
}

// Clear removes the heartbeat, for `uninstall`. A stale "never fired since <date>"
// after the hooks were deliberately removed would be a false alarm.
func Clear(outDir string) { _ = os.Remove(Path(outDir)) }

// now is a variable so tests can pin it.
var now = func() time.Time { return time.Now().UTC().Truncate(time.Second) }

// mutate applies fn to the current state and writes it back atomically.
//
// Read-modify-write without locking is deliberate. Several agent sessions on one
// repository is the documented normal case, so a concurrent update can lose a count —
// which costs nothing, because the question this answers is "has it fired at all, and
// when last", not "exactly how many times". What must NOT happen is a torn file, and
// the temp-plus-rename below makes that impossible: a reader sees either the old file
// or the new one. Taking a lock here would mean a hook waiting on another hook, which
// is exactly what these hooks are built never to do.
func mutate(outDir string, fn func(*State)) {
	if outDir == "" {
		return
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return
	}
	s := Load(outDir)
	fn(&s)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')

	// Staged in the same directory so the rename stays on one filesystem: across a
	// mount boundary os.Rename fails with EXDEV and the atomicity is lost.
	tmp, err := os.CreateTemp(outDir, "."+FileName+".tmp-")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, Path(outDir)); err != nil {
		_ = os.Remove(tmpName)
	}
}
