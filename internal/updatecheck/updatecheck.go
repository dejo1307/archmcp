// Package updatecheck tells people a newer enola exists, without ever putting a
// network call on the path of a command they are waiting for.
//
// enola ships several releases a day. Nothing used to say so: `enola upgrade` worked,
// but only for someone who already knew to run it, and the only way to find that out
// was to visit GitHub. Meanwhile an out-of-date build is not merely missing features —
// when the extractors have moved, it produces a graph with facts a current build would
// have extracted, which is a correctness problem wearing a housekeeping problem's
// clothes.
//
// TWO THINGS THIS DELIBERATELY DOES NOT DO.
//
// It does not check on demand. Every read here is a read of a cached file; the only
// function that touches the network is Refresh, and it is called from exactly two
// places that are already off the critical path (the detached session-start child, and
// the MCP server's boot goroutine). No command a person waits for ever blocks on this,
// and a machine with no network is indistinguishable from one that is up to date.
//
// It does not say what changed. That was tried on paper and does not survive contact
// with the release stream: titles are narrow ("Scala support", "CI fixes"), so one
// headline undersells a ten-version gap and oversells a one-version gap — and the
// changes that matter most are invisible to any signal cheap enough to derive. A merge
// adding Dart while fixing Go Gin routing reads as "adds Dart" to the Go user who
// needed the other half. So the message is generic, and carries exactly one derived
// bit: whether the EXTRACTOR version moved. That bit costs nothing (it is a constant
// already recorded in every snapshot as facts.SnapshotMeta.ExtractorVersion), never
// misreports, and catches the Gin case for free — an extraction fix has to bump it.
package updatecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/enola-labs/enola/internal/filelock"
	"github.com/enola-labs/enola/internal/version"
)

// This package must stay a stdlib-plus-two-leaves package. In particular it must NOT
// import internal/engine: that pulls in the CGO tree-sitter grammars, and this is
// imported by the hook, which has to start and finish in milliseconds. The extractor
// version is therefore PASSED IN by callers that already depend on the engine, rather
// than read from it here.

// manifestURL is the release manifest published by .github/workflows/release.yml.
//
// The /releases/latest/download/ form always resolves to the newest PUBLISHED release,
// which is what makes this safe against a half-finished one: the release workflow keeps
// a release in draft until every platform asset is verified, and drafts are excluded
// here. It is also a plain CDN redirect rather than an API call, so unlike the
// api.github.com path internal/upgrade uses, it carries no 60-requests-per-hour limit
// that a shared IP could exhaust on someone else's behalf.
//
// A variable so tests can point it at an httptest server.
var manifestURL = "https://github.com/enola-labs/enola/releases/latest/download/version.json"

const (
	// ttl bounds how often Refresh will actually fetch. Half a day against a
	// several-a-day release cadence: short enough that nobody stays unaware for long,
	// long enough that the request is unmeasurable.
	ttl = 12 * time.Hour

	// fetchTimeout caps the whole fetch. Refresh runs unattended in a detached child
	// with nobody to interrupt it, so it must be the one to give up.
	fetchTimeout = 10 * time.Second

	// maxManifest bounds the response. The real file is under 100 bytes; anything
	// remotely near this is not our manifest.
	maxManifest = 64 << 10

	dirName  = ".enola"
	fileName = "update.json"
	lockName = "update-check"

	// DisableEnv turns the whole thing off, checks and notices alike.
	DisableEnv = "ENOLA_NO_UPDATE_CHECK"
)

// Manifest is the published shape, and also the on-disk cache's payload. One type for
// both because they are the same information; a second type would only create a place
// for them to disagree.
type Manifest struct {
	// Version is the latest released build, without a leading "v".
	Version string `json:"version"`
	// ExtractorVersion is internal/engine.cacheVersion as of that release: what that
	// build EXTRACTS LIKE, as opposed to what it is called.
	ExtractorVersion string `json:"extractor_version,omitempty"`
}

// state is the cache file, ~/.enola/update.json.
type state struct {
	CheckedAt time.Time `json:"checked_at"`
	Manifest
}

// Notice is the verdict for one build: everything a caller needs to render a message,
// and nothing about how to word it. Available is the only field worth branching on.
type Notice struct {
	// Available is false whenever there is nothing to say — no cache, an unreadable
	// one, a version that does not parse, or a current build. Callers may treat the
	// zero value as silence.
	Available bool `json:"available"`
	// Current is the running build; Latest is what the manifest advertises.
	Current string `json:"current,omitempty"`
	Latest  string `json:"latest,omitempty"`
	// ExtractorMoved reports that the extractors changed between the two, so the graph
	// this build produces is missing facts the latest one would extract. It is the
	// difference between a suggestion and a statement about the data in front of you.
	ExtractorMoved bool `json:"extractor_moved,omitempty"`
}

// Suppressed reports whether this environment must never be told about updates.
//
// Three reasons, none of them about preference:
//
//   - ENOLA_NO_UPDATE_CHECK is the opt-out. Set to anything.
//   - CI is set. A build pipeline cannot act on the advice and did not ask for it, and
//     a tool that chatters in CI logs is one people pin and stop upgrading.
//   - The build is "dev". A local build is AHEAD of the last release, not behind it, so
//     telling its author to upgrade would be both wrong and constant. (internal/upgrade
//     treats "dev" as out of date, which is right for a command someone typed and wrong
//     for an unprompted nudge.)
func Suppressed() bool {
	return os.Getenv(DisableEnv) != "" || os.Getenv("CI") != "" || version.Version == "dev"
}

// Refresh fetches the manifest and caches it. It is the ONLY function here that touches
// the network.
//
// Everything about it is best-effort and silent, in the manner of hookstate: it runs
// where nobody is reading output, so an error it cannot fix is an error it must not
// report. Failing means the cache stays as it was, and a stale cache is exactly as
// harmless as no cache.
//
// It fetches at most once per ttl, and at most one process at a time — a non-blocking
// lock, so the losers return instantly rather than queueing to make a request that has
// already been made. Several agent terminals on one machine is the normal case.
func Refresh(ctx context.Context) {
	if Suppressed() {
		return
	}
	path, err := cachePath()
	if err != nil {
		return
	}
	if fresh(path) {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	lock, ok, err := filelock.TryAcquire(filepath.Join(filepath.Dir(path), lockName))
	if err != nil || !ok {
		return // someone else is already fetching, or we cannot lock; either way, not ours
	}
	defer lock.Release()

	// Re-checked under the lock: between the check above and here, the process we lost
	// the race to may have finished and written a fresh cache.
	if fresh(path) {
		return
	}

	m, err := fetch(ctx)
	if err != nil {
		return
	}
	write(path, state{CheckedAt: time.Now().UTC().Truncate(time.Second), Manifest: m})
}

// For reports what to tell a build running extractorVersion. It reads only the cache
// and never blocks.
//
// extractorVersion is passed in rather than imported for the reason at the top of this
// file. Passing "" is legitimate and simply means ExtractorMoved cannot be determined,
// which is reported as false: an unproven escalation must not be made.
func For(extractorVersion string) Notice {
	if Suppressed() {
		return Notice{}
	}
	path, err := cachePath()
	if err != nil {
		return Notice{}
	}
	s, err := read(path)
	if err != nil {
		return Notice{}
	}

	current := strings.TrimPrefix(version.Version, "v")
	latest := strings.TrimPrefix(s.Version, "v")
	if !newer(current, latest) {
		return Notice{}
	}
	return Notice{
		Available:      true,
		Current:        current,
		Latest:         latest,
		ExtractorMoved: extractorVersion != "" && s.ExtractorVersion != "" && s.ExtractorVersion != extractorVersion,
	}
}

// HumanLine is the message for someone at a terminal, or "" when there is nothing to
// say. It ends in an imperative: they typed a command, they are at a shell, and hedging
// at them ("consider upgrading") wastes the one line this gets.
//
// Callers must print it to STDERR. Several of them — `check --json`, `--version --json`
// — have machine-readable stdout, and a notice is not part of any of those contracts.
func HumanLine(extractorVersion string) string {
	n := For(extractorVersion)
	if !n.Available {
		return ""
	}
	msg := fmt.Sprintf("enola v%s is available (you have v%s)", n.Latest, n.Current)
	if n.ExtractorMoved {
		msg += " — extractors changed since your build, so this graph is missing facts a current enola would extract"
	}
	return msg + ". Run `enola upgrade`."
}

// Fprint writes HumanLine to w, set off by a blank line, when there is something to
// say — and writes nothing at all otherwise, so a caller can invoke it unconditionally
// at the end of a command without guarding it or leaving a stray newline behind.
//
// w should be os.Stderr for every caller. See HumanLine.
func Fprint(w io.Writer, extractorVersion string) {
	if line := HumanLine(extractorVersion); line != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", line)
	}
}

// AgentLine is the message for a model reading a tool result, or "" when there is
// nothing to say.
//
// It is NOT HumanLine, and the difference is the entire point of having two functions.
// An agent told to "run `enola upgrade`" will run it: mid-task, on a machine it was
// not asked to modify, in the middle of somebody else's work. And it would not even
// achieve anything — the upgrade renames a new binary over the old path, so the server
// already running keeps serving from the old inode, and the agent would report a
// version change that did not happen to the tools it is calling.
//
// So this states the fact, assigns the action to the user, and names no command. The
// wording is pinned by a test, because "it currently doesn't say run" is not a property
// that survives editing unless something enforces it.
func AgentLine(extractorVersion string) string {
	n := For(extractorVersion)
	if !n.Available {
		return ""
	}
	msg := fmt.Sprintf("ℹ️ A newer enola is available (v%s; this server runs v%s).", n.Latest, n.Current)
	if n.ExtractorMoved {
		msg += " Extractors changed since this build, so this graph is missing facts a current enola would extract."
	}
	return msg + " Mention this to the user, who decides whether to upgrade. Do not upgrade enola yourself:" +
		" it would not affect this already-running server."
}

// fresh reports whether the cache was written within ttl.
func fresh(path string) bool {
	s, err := read(path)
	if err != nil {
		return false
	}
	return time.Since(s.CheckedAt) < ttl
}

// fetch downloads and decodes the manifest.
func fetch(ctx context.Context) (Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return Manifest{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Manifest{}, fmt.Errorf("HTTP %s", resp.Status)
	}

	var m Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifest)).Decode(&m); err != nil {
		return Manifest{}, err
	}
	if m.Version == "" {
		return Manifest{}, errors.New("manifest has no version")
	}
	return m, nil
}

// cachePath resolves ~/.enola/update.json, mirroring where the graph receipt lives. An
// unavailable home directory (a sandbox with no $HOME) is an error rather than a
// fallback, so callers degrade to silence instead of scattering state.
func cachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, dirName, fileName), nil
}

func read(path string) (state, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return state{}, err
	}
	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return state{}, err // a corrupt cache is worth less than no cache
	}
	return s, nil
}

// write persists the cache atomically. Staged in the same directory so the rename stays
// on one filesystem; across a mount boundary it would fail with EXDEV and lose its
// atomicity. A reader always sees either the old file or the new one, never a torn one.
func write(path string, s state) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+fileName+".tmp-")
	if err != nil {
		return
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
	}
}

// newer reports whether latest is a strictly later release than current.
//
// It FAILS CLOSED: anything it cannot parse with confidence returns false, so a
// malformed or unexpected manifest produces silence rather than a notice that can never
// be satisfied by upgrading. The failure mode of being too strict is missing one
// release; the failure mode of being too loose is telling everybody, forever, to
// upgrade to something they already have.
func newer(current, latest string) bool {
	c, ok := parse(current)
	if !ok {
		return false
	}
	l, ok := parse(latest)
	if !ok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parse reads MAJOR.MINOR.PATCH into three numbers. Pre-release and build suffixes
// (1.2.3-rc1, 1.2.3+meta) are rejected rather than truncated: a release train that
// starts publishing them is a change this comparison should be revisited for, not one
// it should silently guess at.
func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
