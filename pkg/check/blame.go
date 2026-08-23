package check

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// WitnessAge answers when a witness line was last changed, from git's author
// date, or names the cause it cannot: AgeNoGit, AgeUncommitted, or AgeShallow
// when the line reaches the boundary of a shallow clone and that boundary is
// newer than the date asked about. A zero cause means the time is real.
type WitnessAge func(file string, line int, date time.Time) (at time.Time, cause string)

const (
	AgeNoGit       = "no_git"
	AgeUncommitted = "uncommitted"
	AgeShallow     = "shallow"
)

const blameCacheVersion = "1"

type blameLine struct {
	At          int64 `json:"at"`
	Boundary    bool  `json:"boundary,omitempty"`
	Uncommitted bool  `json:"uncommitted,omitempty"`
}

type blameCache struct {
	Version string                          `json:"version"`
	Files   map[string]map[string]blameLine `json:"files"`
}

// BlameReader reads witness line ages through one `git blame --porcelain` per
// file per run and remembers them by the file's blob hash under the
// repository's output directory, so a check on an unchanged file asks git for
// nothing. Blame follows whitespace-insensitive lines (-w); author time is
// what it reports, never commit time.
type BlameReader struct {
	repo      string
	cachePath string
	cache     blameCache
	loaded    map[string]map[int]blameLine
	shallow   *bool
	gitOK     *bool
	dirty     bool
}

func NewBlameReader(repoPath, outDir string) *BlameReader {
	r := &BlameReader{repo: repoPath, cachePath: filepath.Join(outDir, "blame_cache.json"), loaded: map[string]map[int]blameLine{}}
	r.cache = blameCache{Version: blameCacheVersion, Files: map[string]map[string]blameLine{}}
	if raw, err := os.ReadFile(r.cachePath); err == nil {
		var stored blameCache
		if json.Unmarshal(raw, &stored) == nil && stored.Version == blameCacheVersion && stored.Files != nil {
			r.cache = stored
		}
	}
	return r
}

// Age implements WitnessAge for the reader's repository.
func (r *BlameReader) Age(file string, line int, date time.Time) (time.Time, string) {
	if !r.hasGit() {
		return time.Time{}, AgeNoGit
	}
	path, ok := r.locate(file)
	if !ok {
		return time.Time{}, AgeNoGit
	}
	lines, ok := r.linesOf(path)
	if !ok {
		return time.Time{}, AgeNoGit
	}
	entry, ok := lines[line]
	if !ok {
		return time.Time{}, AgeNoGit
	}
	if entry.Uncommitted {
		return time.Time{}, AgeUncommitted
	}
	at := time.Unix(entry.At, 0).UTC()
	if entry.Boundary && r.isShallow() && !at.Before(date) {
		return time.Time{}, AgeShallow
	}
	return at, ""
}

// Save writes what the run read so the next check on the same blobs reads no
// git. A reader that read nothing new writes nothing.
func (r *BlameReader) Save() error {
	if !r.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.cachePath), 0o755); err != nil {
		return err
	}
	out, err := json.Marshal(r.cache)
	if err != nil {
		return err
	}
	return os.WriteFile(r.cachePath, out, 0o644)
}

func (r *BlameReader) hasGit() bool {
	if r.gitOK == nil {
		err := exec.Command("git", "-C", r.repo, "rev-parse", "--is-inside-work-tree").Run()
		ok := err == nil
		r.gitOK = &ok
	}
	return *r.gitOK
}

func (r *BlameReader) isShallow() bool {
	if r.shallow == nil {
		out, err := exec.Command("git", "-C", r.repo, "rev-parse", "--is-shallow-repository").Output()
		shallow := err == nil && strings.TrimSpace(string(out)) == "true"
		r.shallow = &shallow
	}
	return *r.shallow
}

// locate maps an evidence file to a path inside the repository. Evidence
// paths are repository-relative; in a cluster snapshot they may carry the
// member's label as a first segment, which is tried second.
func (r *BlameReader) locate(file string) (string, bool) {
	if file == "" {
		return "", false
	}
	candidates := []string{file}
	if i := strings.IndexByte(file, '/'); i > 0 {
		candidates = append(candidates, file[i+1:])
	}
	for _, c := range candidates {
		if info, err := os.Stat(filepath.Join(r.repo, c)); err == nil && !info.IsDir() {
			return c, true
		}
	}
	return "", false
}

func (r *BlameReader) linesOf(path string) (map[int]blameLine, bool) {
	if lines, ok := r.loaded[path]; ok {
		return lines, lines != nil
	}
	blob, err := exec.Command("git", "-C", r.repo, "hash-object", "--", path).Output()
	key := strings.TrimSpace(string(blob))
	if err == nil && key != "" {
		if stored, ok := r.cache.Files[key]; ok {
			lines := map[int]blameLine{}
			for n, entry := range stored {
				if i, err := strconv.Atoi(n); err == nil {
					lines[i] = entry
				}
			}
			r.loaded[path] = lines
			return lines, true
		}
	}
	lines, ok := r.blame(path)
	if !ok {
		r.loaded[path] = nil
		return nil, false
	}
	r.loaded[path] = lines
	if key != "" {
		stored := map[string]blameLine{}
		for n, entry := range lines {
			stored[strconv.Itoa(n)] = entry
		}
		r.cache.Files[key] = stored
		r.dirty = true
	}
	return lines, true
}

// blame reads one porcelain blame for the whole file. The output is handled as
// bytes and never decoded as text: author names carry any encoding the
// repository's history does, and only the header, author-time and boundary
// lines are read.
func (r *BlameReader) blame(path string) (map[int]blameLine, bool) {
	out, err := exec.Command("git", "-C", r.repo, "blame", "--porcelain", "-w", "--", path).Output()
	if err != nil {
		return nil, false
	}
	lineCommit := map[int]string{}
	commits := map[string]*blameLine{}
	var current *blameLine
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		if len(raw) == 0 {
			continue
		}
		if raw[0] == '\t' {
			current = nil
			continue
		}
		fields := bytes.Fields(raw)
		if len(fields) >= 3 && len(fields[0]) == 40 && isHex(fields[0]) {
			sha := string(fields[0])
			final, err := strconv.Atoi(string(fields[2]))
			if err != nil {
				continue
			}
			entry, known := commits[sha]
			if !known {
				entry = &blameLine{Uncommitted: strings.Trim(sha, "0") == ""}
				commits[sha] = entry
			}
			current = entry
			lineCommit[final] = sha
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case bytes.HasPrefix(raw, []byte("author-time ")):
			if n, err := strconv.ParseInt(string(bytes.TrimSpace(raw[len("author-time "):])), 10, 64); err == nil {
				current.At = n
			}
		case bytes.Equal(raw, []byte("boundary")):
			current.Boundary = true
		}
	}
	lines := make(map[int]blameLine, len(lineCommit))
	for n, sha := range lineCommit {
		lines[n] = *commits[sha]
	}
	return lines, true
}

func isHex(b []byte) bool {
	for _, c := range b {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
