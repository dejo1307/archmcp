package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ErrNoHistory is returned when a root holds no log at all. It is a normal state — a
// repository that has never been snapshotted with history enabled — so callers report it
// as "nothing recorded yet" rather than as a failure.
var ErrNoHistory = errors.New("no architecture history recorded")

// Read returns every entry in a history root, oldest first.
//
// A TRAILING malformed line is tolerated and dropped: the log is appended to on every
// snapshot, so a crash or a full disk mid-write leaves a partial final line, and refusing
// to read the other 900 entries because of it would make the failure permanent for no
// reason. A malformed line ANYWHERE ELSE is an error naming its number — that is
// corruption rather than an interrupted write, and silently skipping it would hide data
// loss behind a log that still renders.
func Read(root string) ([]Entry, error) {
	path := filepath.Join(root, LogFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoHistory
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(data, path)
}

// Parse decodes a log's bytes. name is used only in error messages. Exported so a
// consumer holding the log from somewhere other than a local file — a fetched artifact,
// a merge of two logs — decodes it through exactly the same rules.
func Parse(data []byte, name string) ([]Entry, error) {
	var entries []Entry
	sc := bufio.NewScanner(bytes.NewReader(data))
	// A single entry is a few hundred bytes, but Repos/Parents/Refs on a large
	// multi-repo graph can push a line past bufio's 64 KiB default, which would
	// otherwise surface as a truncated-line error on exactly the biggest graphs.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// Whether the file's last byte is a newline decides what a bad final line MEANS: with
	// the newline the writer finished, so a bad line is corruption; without it the write
	// was interrupted, which is the tolerated case.
	complete := len(data) > 0 && data[len(data)-1] == '\n'

	line := 0
	var pending []Entry // entries after the last successfully parsed one
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			// Defer the verdict: this is only tolerable if it turns out to be the last
			// line of an incomplete file.
			pending = append(pending, Entry{Seq: line})
			if complete {
				return nil, fmt.Errorf("%s:%d: malformed entry: %w", name, line, err)
			}
			continue
		}
		if len(pending) > 0 {
			// Something unparseable sat BEFORE this good line, so it was not a
			// truncated tail.
			return nil, fmt.Errorf("%s:%d: malformed entry (followed by valid entries, so this is corruption, not an interrupted write)", name, pending[0].Seq)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	return entries, nil
}

// Last returns the most recently appended entry, or false when there is none. The last
// LINE, not the greatest Seq or the latest At: it is what a writer compares against to
// decide whether a new snapshot is worth recording.
func Last(entries []Entry) (Entry, bool) {
	if len(entries) == 0 {
		return Entry{}, false
	}
	return entries[len(entries)-1], true
}

// Merge unions two logs of the same repository, dropping duplicate revisions and
// returning them in a stable order.
//
// This is the whole of the merge algorithm, and it is a property of the format rather
// than of any transport: entries are content-addressed by ID, so the same revision
// observed on two machines is one revision. It is exercised now, with no remote in
// sight, because it is what pins down the three format rules that make a later merge
// possible at all — identity by ID, repo by portable identity, and Seq as local
// bookkeeping that must never be used for ordering.
//
// Ordering is by At, then by ID as a tiebreak so the result is deterministic when two
// machines record the same second. Seq is renumbered locally: it describes THIS
// machine's log, so carrying the other side's would produce duplicates and gaps.
func Merge(a, b []Entry) []Entry {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]Entry, 0, len(a)+len(b))
	for _, src := range [][]Entry{a, b} {
		for _, e := range src {
			if e.ID != "" {
				if _, dup := seen[e.ID]; dup {
					continue
				}
				seen[e.ID] = struct{}{}
			}
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].At != out[j].At {
			return out[i].At < out[j].At
		}
		return out[i].ID < out[j].ID
	})
	for i := range out {
		out[i].Seq = i + 1
	}
	return out
}

// Resolve maps a revision selector to an entry.
//
// Accepted forms, in the order they are tried:
//
//	""            the newest revision
//	latest        the newest revision
//	HEAD          the newest revision
//	HEAD~N        N revisions back from the newest
//	@N            the revision with Seq == N
//	<id prefix>   a snapshot ID prefix, with or without "sha256:" (≥4 chars)
//	<commit>      a git commit, or a prefix of one (≥4 chars); the NEWEST revision
//	              taken at that commit, since one commit can have many
//	<ref name>    a name in Refs ("baseline")
//
// An ambiguous prefix is an error naming the candidates rather than a silent pick.
func Resolve(entries []Entry, sel string) (Entry, error) {
	if len(entries) == 0 {
		return Entry{}, ErrNoHistory
	}
	s := strings.TrimSpace(sel)
	switch {
	case s == "" || strings.EqualFold(s, "latest") || s == "HEAD":
		return entries[len(entries)-1], nil
	case strings.HasPrefix(s, "HEAD~"):
		n, err := strconv.Atoi(s[len("HEAD~"):])
		if err != nil || n < 0 {
			return Entry{}, fmt.Errorf("bad revision %q: expected HEAD~<number>", sel)
		}
		i := len(entries) - 1 - n
		if i < 0 {
			return Entry{}, fmt.Errorf("revision %q is before the start of the history (%d revisions recorded)", sel, len(entries))
		}
		return entries[i], nil
	case strings.HasPrefix(s, "@"):
		n, err := strconv.Atoi(s[1:])
		if err != nil {
			return Entry{}, fmt.Errorf("bad revision %q: expected @<seq>", sel)
		}
		for _, e := range entries {
			if e.Seq == n {
				return e, nil
			}
		}
		return Entry{}, fmt.Errorf("no revision with seq %d", n)
	}

	// Named refs are exact and few, so they win over any prefix interpretation.
	for i := len(entries) - 1; i >= 0; i-- {
		for _, r := range entries[i].Refs {
			if r == s {
				return entries[i], nil
			}
		}
	}

	if len(s) < 4 {
		return Entry{}, fmt.Errorf("revision %q is too short to identify anything — use at least 4 characters", sel)
	}
	needle := strings.ToLower(strings.TrimPrefix(s, "sha256:"))

	var byID []Entry
	for _, e := range entries {
		if strings.HasPrefix(strings.TrimPrefix(e.ID, "sha256:"), needle) {
			byID = append(byID, e)
		}
	}
	if n := len(byID); n == 1 {
		return byID[0], nil
	} else if n > 1 {
		return Entry{}, ambiguous(sel, byID)
	}

	// A commit can hold several revisions (a working revision per edit round, plus the
	// committed one), so this resolves to the newest rather than reporting ambiguity —
	// "the architecture at commit X" means the last thing observed there.
	for i := len(entries) - 1; i >= 0; i-- {
		if c := entries[i].Commit(); c != "" && strings.HasPrefix(c, needle) {
			return entries[i], nil
		}
	}
	return Entry{}, fmt.Errorf("no revision matches %q", sel)
}

func ambiguous(sel string, matches []Entry) error {
	var ids []string
	for _, e := range matches {
		if len(ids) == 5 {
			ids = append(ids, "…")
			break
		}
		ids = append(ids, e.Short())
	}
	return fmt.Errorf("revision %q is ambiguous — matches %s", sel, strings.Join(ids, ", "))
}
