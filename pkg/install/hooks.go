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
	// Matcher, when set, means this event takes matcher-GROUPED entries rather than a
	// flat list. The two shapes are not interchangeable: writing one where the other is
	// expected produces a config that parses cleanly and never fires.
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
		"change introduced a regression, and only if a baseline was pinned",
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

		// Stop takes a flat list of entries; SessionStart takes matcher-grouped ones.
		// The events genuinely differ in shape, and getting it wrong is the silent kind
		// of failure: the config parses and the hook simply never fires.
		for _, h := range installedHooks {
			entry := map[string]any{
				"type":    "command",
				"command": o.hookCommand() + " " + h.Subcommand,
				"timeout": hookTimeoutSeconds,
				"source":  enolaHookMarker,
			}
			if h.Matcher != "" {
				hooks[h.Event] = mergeMatcher(hooks[h.Event], h.Matcher, entry, remove)
			} else {
				hooks[h.Event] = mergeFlat(hooks[h.Event], entry, remove)
			}
		}

		for _, k := range []string{"Stop", "SessionStart"} {
			if lst, ok := hooks[k].([]any); ok && len(lst) == 0 {
				delete(hooks, k)
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

// mergeFlat adds or removes one entry in a flat hook list, preserving every other entry.
func mergeFlat(existing any, entry map[string]any, remove bool) any {
	list, _ := existing.([]any)
	out := make([]any, 0, len(list)+1)
	for _, e := range list {
		if !isEnolaEntry(e) {
			out = append(out, e)
		}
	}
	if !remove {
		out = append(out, entry)
	}
	return out
}

// mergeMatcher adds or removes one entry inside a matcher-grouped hook list. An existing
// group with the same matcher is reused so the user does not accumulate duplicate groups;
// groups belonging to anything else are left exactly as they were.
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
		inner, _ := group["hooks"].([]any)
		kept := make([]any, 0, len(inner))
		for _, e := range inner {
			if !isEnolaEntry(e) {
				kept = append(kept, e)
			}
		}
		if !remove && group["matcher"] == matcher {
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
		out = append(out, map[string]any{
			"matcher": matcher,
			"hooks":   []any{entry},
		})
	}
	return out
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
