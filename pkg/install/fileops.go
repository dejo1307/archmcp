// Package install writes enola's agent instructions, and optionally its hooks, into
// the configuration files the coding agents on this machine actually read.
//
// Everything here is built around one rule: these are the user's files, and a tool that
// mangles them gets uninstalled. Two competitors have shipped bugs in exactly this area —
// one deleted hand-written content while updating its own section, the other wrote to a
// path its target never reads, so the integration silently did nothing. The primitives in
// this file exist to make both classes impossible rather than unlikely.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Action is what happened to one file.
type Action string

const (
	ActionCreated   Action = "created"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionRemoved   Action = "removed"
	ActionSkipped   Action = "skipped"
)

// Result reports one file's outcome, so the user is told exactly what was touched.
// Reason is set when the outcome needs explaining (skipped, or a backup was taken).
type Result struct {
	Path   string `json:"path"`
	Action Action `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// Sentinels delimiting an enola-owned block inside a file the user also edits. HTML
// comments because the shared targets are markdown, and because Claude Code strips
// block-level comments before loading a file into context — so the markers cost the
// user nothing.
const (
	beginMarker = "<!-- enola:begin -->"
	endMarker   = "<!-- enola:end -->"
)

// atomicWrite writes content to path via a temp file in the same directory, then
// renames. A crash mid-write can therefore leave the old file or the new one, never a
// truncated mixture of the two — and these are files an agent session depends on.
func atomicWrite(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("staging %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("setting mode on %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publishing %s: %w", path, err)
	}
	return nil
}

// writeOwnedFile publishes a file enola owns outright — a Claude Code rule, a Cursor
// rule. There is no merging to do and nothing of the user's to preserve, so the only
// requirements are that an unchanged file is reported as unchanged rather than rewritten,
// and that the write is atomic.
func writeOwnedFile(path, content string, dryRun bool) (Result, error) {
	existing, err := os.ReadFile(path)
	switch {
	case err == nil && string(existing) == content:
		return Result{Path: path, Action: ActionUnchanged}, nil
	case err == nil:
		if dryRun {
			return Result{Path: path, Action: ActionUpdated}, nil
		}
		if err := atomicWrite(path, []byte(content), 0o644); err != nil {
			return Result{}, err
		}
		return Result{Path: path, Action: ActionUpdated}, nil
	case os.IsNotExist(err):
		if dryRun {
			return Result{Path: path, Action: ActionCreated}, nil
		}
		if err := atomicWrite(path, []byte(content), 0o644); err != nil {
			return Result{}, err
		}
		return Result{Path: path, Action: ActionCreated}, nil
	default:
		return Result{}, fmt.Errorf("reading %s: %w", path, err)
	}
}

// upsertSection replaces, appends, or leaves alone an enola-owned block inside a file the
// user also maintains.
//
// The block is delimited by explicit sentinels and the boundary is NEVER inferred from
// document structure. graphify's equivalent anchored on a heading and took everything up
// to the next one, so an inline mention of its own name caused it to delete the lines
// between — "silently destroying hand-curated content". A sentinel pair cannot do that:
// either both markers are present and the span between them is ours, or they are not and
// we append.
//
// Unbalanced markers are refused rather than guessed at. A file with a begin and no end
// has been hand-edited in a way we cannot safely interpret, and the correct response to
// not understanding a user's file is to stop.
func upsertSection(path, block string, dryRun bool) (Result, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if dryRun {
			return Result{Path: path, Action: ActionCreated}, nil
		}
		if err := atomicWrite(path, []byte(block+"\n"), 0o644); err != nil {
			return Result{}, err
		}
		return Result{Path: path, Action: ActionCreated}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("reading %s: %w", path, err)
	}

	content := string(raw)
	start := strings.Index(content, beginMarker)
	end := strings.Index(content, endMarker)

	switch {
	case start < 0 && end < 0:
		updated := strings.TrimRight(content, "\n") + "\n\n" + block + "\n"
		return applySection(path, content, updated, ActionUpdated, dryRun)
	case start < 0 || end < 0 || end < start:
		return Result{
			Path:   path,
			Action: ActionSkipped,
			Reason: "enola's markers are unbalanced or out of order in this file; refusing to guess where its section ends — fix or remove them and re-run",
		}, nil
	default:
		updated := content[:start] + block + content[end+len(endMarker):]
		return applySection(path, content, updated, ActionUpdated, dryRun)
	}
}

// removeSection strips enola's block, leaving everything else byte-identical.
func removeSection(path string, dryRun bool) (Result, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Result{Path: path, Action: ActionUnchanged}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("reading %s: %w", path, err)
	}
	content := string(raw)
	start := strings.Index(content, beginMarker)
	end := strings.Index(content, endMarker)
	if start < 0 || end < 0 || end < start {
		return Result{Path: path, Action: ActionUnchanged}, nil
	}

	updated := strings.TrimRight(content[:start], "\n")
	tail := content[end+len(endMarker):]
	if t := strings.TrimLeft(tail, "\n"); t != "" {
		updated += "\n\n" + t
	} else if updated != "" {
		updated += "\n"
	}

	// A file that exists only because enola created it is removed outright rather than
	// left behind as an empty artifact of a tool that is no longer installed.
	if strings.TrimSpace(updated) == "" {
		if dryRun {
			return Result{Path: path, Action: ActionRemoved}, nil
		}
		if err := os.Remove(path); err != nil {
			return Result{}, fmt.Errorf("removing %s: %w", path, err)
		}
		return Result{Path: path, Action: ActionRemoved}, nil
	}
	return applySection(path, content, updated, ActionUpdated, dryRun)
}

func applySection(path, before, after string, action Action, dryRun bool) (Result, error) {
	if before == after {
		return Result{Path: path, Action: ActionUnchanged}, nil
	}
	if dryRun {
		return Result{Path: path, Action: action}, nil
	}
	if err := atomicWrite(path, []byte(after), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Path: path, Action: action}, nil
}

// mutateJSON reads a JSON config, hands it to mutate, and writes it back only if mutate
// actually changed something.
//
// Three properties matter, all of them about not damaging a file enola does not own:
//
//   - Unparseable input is BACKED UP and skipped, never overwritten. A file we cannot
//     read is one we cannot safely rewrite, and it may be the user's only copy.
//   - Deep equality decides whether to write, so re-running reports "unchanged" instead
//     of churning the file and its mtime.
//   - mutate receives the whole document and edits in place, so every key enola does not
//     know about survives untouched.
func mutateJSON(path string, mutate func(doc map[string]any), dryRun bool) (Result, error) {
	doc := map[string]any{}
	existed := false

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		existed = true
		if err := json.Unmarshal(raw, &doc); err != nil {
			backup := path + ".enola-backup"
			if !dryRun {
				_ = os.WriteFile(backup, raw, 0o644)
			}
			return Result{
				Path:   path,
				Action: ActionSkipped,
				Reason: fmt.Sprintf("not valid JSON (%v); left untouched, copy saved to %s", err, filepath.Base(backup)),
			}, nil
		}
	case !os.IsNotExist(err):
		return Result{}, fmt.Errorf("reading %s: %w", path, err)
	}

	before, err := json.Marshal(doc)
	if err != nil {
		return Result{}, fmt.Errorf("encoding %s: %w", path, err)
	}
	mutate(doc)
	after, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("encoding %s: %w", path, err)
	}
	// Compare canonically: MarshalIndent differs from Marshal in whitespace only, so
	// re-marshal compactly to decide whether anything actually changed.
	afterCompact, err := json.Marshal(doc)
	if err != nil {
		return Result{}, fmt.Errorf("encoding %s: %w", path, err)
	}
	if string(before) == string(afterCompact) && existed {
		return Result{Path: path, Action: ActionUnchanged}, nil
	}
	// Nothing to say and no file to say it in. Without this, uninstalling from a project
	// that never had hooks CREATED an empty settings.json — and with it the .claude
	// directory — so removing enola left more behind than installing it without hooks did.
	if !existed && len(doc) == 0 {
		return Result{Path: path, Action: ActionUnchanged}, nil
	}

	action := ActionUpdated
	if !existed {
		action = ActionCreated
	}
	if dryRun {
		return Result{Path: path, Action: action}, nil
	}
	if err := atomicWrite(path, append(after, '\n'), 0o644); err != nil {
		return Result{}, err
	}
	return Result{Path: path, Action: action}, nil
}
