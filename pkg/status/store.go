package status

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// Usage stats are stored per-repo under the user's home directory so they
// survive deletion of the repo (and its ephemeral .enola/ snapshot dir):
//
//	~/.enola/usage/<repo-base>-<hash8>.json
//
// Keying by absolute repo path means each server writes only its own file (no
// cross-repo write contention), and the folder can be aggregated by reading it.

// canonicalRepoPath normalizes a repo path so the same repo always maps to the
// same usage key regardless of how it was referenced — making it absolute and
// resolving symlinks (e.g. macOS /var → /private/var). Falls back gracefully if
// the path can't be resolved (e.g. it doesn't exist yet).
func canonicalRepoPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// usageDir returns the directory holding per-repo usage files.
func usageDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".enola", "usage"), nil
}

// usageKey derives a stable, human-readable filename stem for a repo from its
// absolute path: the sanitized base name plus a short hash of the full path
// (so two repos with the same base name don't collide).
func usageKey(absRepoPath string) string {
	sum := sha256.Sum256([]byte(absRepoPath))
	short := hex.EncodeToString(sum[:])[:8]
	base := sanitizeBase(filepath.Base(absRepoPath))
	if base == "" {
		base = "repo"
	}
	return base + "-" + short
}

// usagePath returns the per-repo usage file path under usageDir().
func usagePath(absRepoPath string) (string, error) {
	dir, err := usageDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, usageKey(absRepoPath)+".json"), nil
}

// legacyPath returns the old in-repo status file location, used for migration
// and back-compat reads.
func legacyPath(absRepoPath string) string {
	return filepath.Join(absRepoPath, ".enola", StatusFile)
}

// sanitizeBase keeps a repo base name safe for use as a filename stem.
func sanitizeBase(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}
