package install

import (
	"path/filepath"
)

// hookTimeoutSeconds bounds each hook. Both hooks are designed to return promptly —
// session-start detaches its work, stop exits immediately without a baseline — so a
// timeout means something is wrong, and the harness treating that as a non-blocking
// error is exactly the desired outcome: the session proceeds regardless.
const hookTimeoutSeconds = 60

// enolaHookMarker identifies the hook entries enola owns, so uninstall can remove
// precisely those and leave every other hook in the file alone. It rides along as an
// ordinary JSON field; unknown fields in a hook entry are ignored by the harness.
const enolaHookMarker = "enola"

// hookSpec is one hook enola installs. The description lives here, next to the thing it
// describes, because the two drifted apart the moment they were separate: after
// SessionStart was pulled from the installer, the CLI carried on announcing that "a
// baseline is pinned at session start" — the tool describing a mechanism it did not
// install, which is the one thing a config writer must never do.
type hookSpec struct {
	Event      string
	Subcommand string
	// Matcher narrows an event to particular occasions of it — SessionStart fires on
	// startup, resume and clear, and enola wants the first two. An empty Matcher means
	// the event applies unconditionally, NOT that the entry takes a different shape:
	// every event is written as a list of matcher groups, and a group with no matcher
	// simply omits the key. See writeHooks.
	Matcher     string
	Description string
}

// installedHooks is the single source of truth for what --hooks configures. Adding an
// event here updates the writer, the CLI's summary and the instruction text together.
var installedHooks = []hookSpec{{
	Event:      "SessionStart",
	Matcher:    "startup|resume",
	Subcommand: "hook session-start",
	Description: "at the start of a session the architecture is frozen as a baseline, in the " +
		"background — session start is never delayed, and a baseline you pinned yourself is " +
		"never replaced",
}, {
	Event:      "Stop",
	Subcommand: "hook stop",
	Description: "at the end of a session, the architectural delta is reported only if the " +
		"change introduced a regression — or, once per cause, that the baseline was not " +
		"comparable so nothing could be graded at all",
}}

// HookSummary describes what --hooks will actually configure, for callers that need to
// tell the user before writing anything.
func HookSummary() []string {
	out := make([]string, 0, len(installedHooks))
	for _, h := range installedHooks {
		out = append(out, h.Description)
	}
	return out
}

// writeHooks merges enola's hooks into the agent's settings.json, or removes them.
//
// Merging rather than replacing is the whole job: settings.json belongs to the user and
// very likely already contains hooks, permissions and other configuration that must
// survive untouched. Only the entries carrying enolaHookMarker are ever added or removed.
func writeHooks(o Options, remove bool) ([]Result, error) {
	path := filepath.Join(claudeDir(o), "settings.json")

	r, err := mutateJSON(path, func(doc map[string]any) {
		hooks, _ := doc["hooks"].(map[string]any)
		if hooks == nil {
			if remove {
				return
			}
			hooks = map[string]any{}
		}

		// EVERY event is a list of matcher groups, each holding a nested `hooks` array.
		// Events differ in whether they carry a matcher, not in their shape.
		//
		// This code used to write Stop as a flat list of entries, on the stated premise
		// that the two events genuinely differed — and the comment above it predicted
		// its own failure mode exactly: the config parses and the hook simply never
		// fires. It never fired. `SessionStart` pinned a baseline before editing while
		// `Stop` silently graded nothing, which is the worst of the three possible
		// states: everything looks configured and the half that produces the value is
		// absent. Verified against a real session, both shapes, one variable.
		for _, h := range installedHooks {
			entry := map[string]any{
				"type":    "command",
				"command": o.hookCommand() + " " + h.Subcommand,
				"timeout": hookTimeoutSeconds,
				"source":  enolaHookMarker,
			}
			hooks[h.Event] = mergeMatcher(hooks[h.Event], h.Matcher, entry, remove)
		}

		for _, h := range installedHooks {
			if lst, ok := hooks[h.Event].([]any); ok && len(lst) == 0 {
				delete(hooks, h.Event)
			}
		}
		if len(hooks) == 0 {
			delete(doc, "hooks")
			return
		}
		doc["hooks"] = hooks
	}, o.DryRun)
	if err != nil {
		return nil, err
	}
	return []Result{r}, nil
}

// mergeMatcher adds or removes one entry inside an event's list of matcher groups. An
// existing group with the same matcher is reused so the user does not accumulate
// duplicate groups; groups belonging to anything else are left exactly as they were.
//
// An empty matcher means the group that carries no matcher key — the event applying
// unconditionally. Such a group is written without the key and recognised again by the
// same absence, so a second install updates it in place rather than appending a
// duplicate, and uninstall still finds it.
func mergeMatcher(existing any, matcher string, entry map[string]any, remove bool) any {
	groups, _ := existing.([]any)
	out := make([]any, 0, len(groups)+1)
	placed := false

	for _, g := range groups {
		group, ok := g.(map[string]any)
		if !ok {
			out = append(out, g)
			continue
		}
		// A map with no `hooks` key is a bare command entry from a flat list — the
		// shape enola wrote for Stop before this was fixed, which parses and never
		// fires. Ours is dropped, which is how an existing installation migrates.
		// Anyone else's is preserved verbatim: it is their file, and a hook that does
		// not fire is still not ours to delete.
		if _, grouped := group["hooks"]; !grouped {
			if !isEnolaEntry(group) {
				out = append(out, g)
			}
			continue
		}
		inner, _ := group["hooks"].([]any)
		kept := make([]any, 0, len(inner))
		for _, e := range inner {
			if !isEnolaEntry(e) {
				kept = append(kept, e)
			}
		}
		if !remove && matcherOf(group) == matcher {
			kept = append(kept, entry)
			placed = true
		}
		if len(kept) == 0 {
			// The group existed only for enola's entry; drop it rather than leave an
			// empty husk behind.
			continue
		}
		group["hooks"] = kept
		out = append(out, group)
	}

	if !remove && !placed {
		group := map[string]any{"hooks": []any{entry}}
		if matcher != "" {
			group["matcher"] = matcher
		}
		out = append(out, group)
	}
	return out
}

// matcherOf returns a group's matcher, treating an absent or non-string value as the
// empty matcher — the group that applies unconditionally.
//
// Comparing group["matcher"] to a string directly cannot see that group: the value is
// nil, and nil never equals "". Every install would then append another group instead
// of updating the one already there, and uninstall would leave it behind.
func matcherOf(group map[string]any) string {
	s, _ := group["matcher"].(string)
	return s
}

// isEnolaEntry reports whether a hook entry is one enola installed. Identified by an
// explicit marker rather than by matching the command string, so a user who edits the
// command — adding a flag, pointing at a different binary — still gets a clean uninstall.
func isEnolaEntry(e any) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	return m["source"] == enolaHookMarker
}
