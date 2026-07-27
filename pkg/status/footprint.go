package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// charsPerToken is the rough characters-per-token ratio used to convert source
// bytes into an estimated token count. Matches the engine's own heuristic
// (pkg/mcputil/mcputil.go approxTokensPerChar = 4).
const charsPerToken = 4

// snapshotArtifactSubdirs are the rotated/pinned snapshot directories inside
// .enola/ whose sizes are rolled up rather than listed file-by-file.
var snapshotArtifactSubdirs = []string{"previous", "baseline"}

// FileSize is a single entry in the .enola footprint.
type FileSize struct {
	Name  string // file or subdir name (relative to .enola/)
	Bytes int64
	IsDir bool // true for rolled-up subdirs (previous/, baseline/)
}

// Footprint describes how much snapshotted data lives in .enola/, plus the
// stats of the snapshot itself (what the user gets to work with without
// re-parsing the source).
type Footprint struct {
	Files      []FileSize // top-level .enola entries, subdirs rolled up
	TotalBytes int64      // sum of everything under .enola/

	// Snapshot stats (from snapshot.meta.json / receipt.json; zero if absent).
	FilesIndexed int
	FilesParsed  int
	Facts        int
	Insights     int
	SourceBytes  int64 // total bytes of indexed source files (os.Stat sum)
	SourceTokens int   // SourceBytes / charsPerToken
}

// snapshotMeta is a minimal projection of .enola/snapshot.meta.json. We read the
// JSON directly rather than importing internal/facts, so a status report never
// drags the engine's type graph in and stays robust to engine internals.
type snapshotMeta struct {
	RepoPath     string `json:"repo_path"`
	FactCount    int    `json:"fact_count"`
	InsightCount int    `json:"insight_count"`
	FilesSeen    int    `json:"files_seen"`
	FilesParsed  int    `json:"files_parsed"`
	SourceBytes  int64  `json:"source_bytes"`
	FileHashes   []struct {
		Path string `json:"path"`
	} `json:"file_hashes"`
}

// sourceExts is the fallback filter for snapshots written before the engine
// recorded SourceBytes. It approximates "a file an extractor could have parsed";
// the engine's own figure, which knows exactly which files produced facts, is
// always preferred.
var sourceExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".rb": true, ".py": true, ".kt": true, ".kts": true, ".swift": true, ".java": true,
	".rs": true, ".php": true, ".c": true, ".cc": true, ".cpp": true, ".cxx": true,
	".h": true, ".hpp": true, ".hxx": true, ".m": true, ".mm": true,
	".proto": true, ".vue": true, ".erb": true, ".haml": true,
}

// ScanFootprint walks the given .enola directory and reads the snapshot metadata
// to produce a Footprint. It is best-effort: missing or malformed files yield
// zero values rather than errors.
func ScanFootprint(enolaDir string) Footprint {
	var fp Footprint

	entries, err := os.ReadDir(enolaDir)
	if err != nil {
		return fp
	}

	rollup := make(map[string]bool, len(snapshotArtifactSubdirs))
	for _, d := range snapshotArtifactSubdirs {
		rollup[d] = true
	}

	for _, e := range entries {
		full := filepath.Join(enolaDir, e.Name())
		if e.IsDir() {
			size := dirSize(full)
			fp.Files = append(fp.Files, FileSize{Name: e.Name(), Bytes: size, IsDir: true})
			fp.TotalBytes += size
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		fp.Files = append(fp.Files, FileSize{Name: e.Name(), Bytes: info.Size()})
		fp.TotalBytes += info.Size()
	}

	fp.applySnapshotMeta(enolaDir)
	return fp
}

// applySnapshotMeta enriches the footprint with snapshot stats read from
// snapshot.meta.json, including summing the on-disk size of every indexed source
// file to estimate how many tokens of code are snapshotted.
func (fp *Footprint) applySnapshotMeta(enolaDir string) {
	data, err := os.ReadFile(filepath.Join(enolaDir, "snapshot.meta.json"))
	if err != nil {
		return
	}
	var meta snapshotMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return
	}

	fp.Facts = meta.FactCount
	fp.Insights = meta.InsightCount
	fp.FilesParsed = meta.FilesParsed
	fp.FilesIndexed = len(meta.FileHashes)
	if fp.FilesIndexed == 0 {
		fp.FilesIndexed = meta.FilesSeen
	}

	// Prefer the engine's own measurement: it is the size of exactly the files
	// that produced facts. Only fall back to walking file_hashes for snapshots
	// written before that field existed — and even then, filter to plausible
	// source extensions. file_hashes is the files_SEEN set, so summing it whole
	// counts images, media and vendored databases as though an agent would have
	// read them: on one real repo, 121M tokens against 3.1M of actual source.
	if meta.SourceBytes > 0 {
		fp.SourceBytes = meta.SourceBytes
	} else {
		for _, fh := range meta.FileHashes {
			if !sourceExts[strings.ToLower(filepath.Ext(fh.Path))] {
				continue
			}
			p := fh.Path
			if !filepath.IsAbs(p) && meta.RepoPath != "" {
				p = filepath.Join(meta.RepoPath, p)
			}
			info, err := os.Stat(p)
			if err != nil {
				continue
			}
			fp.SourceBytes += info.Size()
		}
	}
	fp.SourceTokens = int(fp.SourceBytes / charsPerToken)
}

// dirSize returns the total size of all regular files under dir (recursive).
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}
