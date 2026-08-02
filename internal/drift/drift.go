// Package drift annotates an architecture delta with the one caveat that invalidates it
// outright: the working tree no longer matching the snapshot it was computed from.
//
// It is its own package because both sides of the module boundary need it and neither
// can reach the other. `internal/server` cannot import `pkg/bootstrap` (bootstrap
// imports the server), and an out-of-module consumer computing its own delta cannot
// import `internal/server` at all. A leaf depending on `engine` and `diff`, depended on
// by both, is the only shape with no cycle — and one implementation is the point: two
// tools disagreeing about what "stale" means, or how much of the tree to name, is itself
// a kind of drift.
package drift

import (
	"fmt"
	"log"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/engine"
)

// samplePaths bounds how many drifted paths a warning names, so the message stays
// readable on a large refactor while still showing WHICH files moved.
const samplePaths = 5

// Source is anything that can report whether a repository's working tree still matches
// the snapshot it holds. Both the internal engine and the public bootstrap.Engine
// satisfy it, which is what lets one implementation serve callers on either side of the
// module boundary without either importing the other.
type Source interface {
	Drift(repoPath string) (engine.Drift, error)
}

// AddWarning appends a comparability caveat to d when repoPath's working tree no longer
// matches the snapshot src holds, so a delta computed from a stale snapshot cannot be
// mistaken for a current one. rerunTool names the tool to call again, since the remedy
// is always "re-snapshot, then repeat whatever you just ran".
//
// It re-hashes the repository, so it belongs at a deliberate decision point — a diff, a
// validation — and not on every tool call. That is why it is a helper a caller invokes
// rather than something the diff does for itself.
//
// A failure to determine drift is logged and otherwise ignored: the delta is still worth
// returning, and inventing a caveat from an unreadable repo would be its own false
// signal. A snapshot with no recorded file hashes reports Unknown, surfaced as "cannot
// verify" rather than silence — an unanswerable check must not read as a clean bill of
// health.
func AddWarning(d *diff.SnapshotDiff, src Source, repoPath, rerunTool string) {
	if d == nil || src == nil || repoPath == "" {
		return
	}
	dr, err := src.Drift(repoPath)
	if err != nil {
		log.Printf("[drift] could not determine drift for %s: %v", repoPath, err)
		return
	}
	switch {
	case dr.Unknown:
		d.AddWarning("could not verify that the current snapshot still matches the working tree — " +
			dr.Summary(samplePaths))
	case dr.Any():
		d.AddWarning(fmt.Sprintf(
			"the working tree has changed since the current snapshot was taken: %s — this result "+
				"describes neither the snapshot's state nor the tree's; re-run generate_snapshot and %s again",
			dr.Summary(samplePaths), rerunTool))
	}
}
