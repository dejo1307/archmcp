package install

import "strings"

// Instructions is the guidance written into every target. It is deliberately short:
// agent instruction files are loaded into context on every session, so every line costs
// the user tokens forever, and the platform's own guidance is that shorter files produce
// better adherence.
//
// It tells the agent only what the agent still has to decide. The verification half of
// the loop is not described as a thing to remember, because with hooks installed it
// happens on its own, and an instruction that duplicates a mechanism is one more thing to
// drift out of date.
const Instructions = `## enola — architecture before and after a change

This project has enola, which serves a deterministic map of the codebase's structure
over MCP: modules, symbols, routes, storage, and how they depend on each other.

Before changing code whose blast radius is not obvious:

- ` + "`impact_analysis`" + ` — what transitively depends on this, before you touch it.
- ` + "`explore`" + ` / ` + "`traverse`" + ` / ` + "`find_path`" + ` — how something is wired, instead of
  reconstructing it by reading files.
- ` + "`set_baseline`" + ` — pin the architecture BEFORE you start editing, so the change can
  be graded afterwards. Do this once, early.

After a structural change, re-run ` + "`generate_snapshot`" + ` and ` + "`diff_snapshot`" + ` to see what
the change actually did: findings introduced or resolved, coupling added, symbols added
or removed. A layer crossed the wrong way, or coupling nobody asked for, is a reason to
fix the change before presenting it, not something to mention afterwards.

Prefer these over re-deriving structure by grepping. They are exact, and they cost a
fraction of the file reading they replace.`

// HooksNote is appended when hooks are installed, so the file explains a mechanism the
// user will otherwise encounter with no idea where it came from — an unexplained message
// at the end of a session is exactly how a tool earns a reputation for being noisy.
const HooksNote = `

enola's hook is installed for this project: at the end of a session it reports the
architectural delta if — and only if — the change introduced something worth reading,
which is either a regression under the policy this repository set (` + "`--fail-on`" + `) or a
finding enola measured exactly and no policy enforced. It never blocks, and it stays
silent when the change is clean.

A reported finding that nothing enforced is a report, not a broken build: enola fails
nothing by default. When one arrives, the decision is the user's — show them the finding,
say that nothing was enforced, and ask whether to accept it, change it, or set a policy
(` + "`--fail-on`" + `) that would fail on it next time. Do not revert work over it on your
own initiative, and do not describe the session as clean without mentioning it.

It speaks in one other case: when it could not grade the change at all, because the
baseline is not comparable to the current snapshot. That is NOT a verdict about your
change — it means no verdict was reached — and the remedy is to re-pin the baseline.
Said once per cause, not once per session.`

// block wraps content in the sentinels used for files the user also maintains.
func block(content string) string {
	return beginMarker + "\n" + strings.TrimSpace(content) + "\n" + endMarker
}

// claudeRule is the body of the Claude Code rule file. No `paths` frontmatter, so it
// loads at launch with the same priority as a project CLAUDE.md.
func claudeRule(o Options) string {
	return header() + body(o) + "\n"
}

// cursorRule carries Cursor's frontmatter. alwaysApply keeps it in context rather than
// leaving it to description-matching.
func cursorRule(o Options) string {
	return "---\nalwaysApply: true\n---\n\n" + header() + body(o) + "\n"
}

// copilotInstructions carries Copilot's frontmatter. applyTo is REQUIRED for a file under
// .github/instructions/ — without it the file governs nothing, which is the silent kind of
// failure: it exists, it looks installed, and it never applies. "**" is what makes it
// unconditional, the counterpart of Cursor's alwaysApply.
func copilotInstructions(o Options) string {
	return "---\napplyTo: \"**\"\n---\n\n" + header() + body(o) + "\n"
}

// header marks the file as generated, so a reader knows why it exists and how to remove
// it without having to search for the tool that wrote it.
func header() string {
	return "<!-- Written by `enola install`. Edit freely; `enola install` will overwrite,\n" +
		"     and `enola uninstall` will remove this file. -->\n\n"
}

// body composes what actually gets written: the shared instructions, the hooks note when
// hooks are installed, and whatever the calling binary appends.
//
// ExtraInstructions is the seam for a wrapper that serves tools the OSS build does not,
// so it can tell the agent they exist without this package having to know about them —
// and without a wrapper having to fork the instruction text to add two lines. Empty for
// every current caller; the shared output is byte-identical while it stays empty.
func body(o Options) string {
	out := Instructions
	if o.Hooks {
		out += HooksNote
	}
	if extra := strings.TrimSpace(o.ExtraInstructions); extra != "" {
		out += "\n\n" + extra
	}
	if o.Hooks {
		if extra := strings.TrimSpace(o.ExtraHooksNote); extra != "" {
			out += "\n\n" + extra
		}
	}
	return out
}
