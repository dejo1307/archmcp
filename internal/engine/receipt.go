package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/facts"
)

// parseErrorSampleCap bounds the number of parse errors retained on the receipt.
const parseErrorSampleCap = 20

// hashPrefix is prepended to every hash value surfaced in the receipt
// (snapshot_id, config/glob hashes, output hashes), matching the notation issue
// #60 requested and making the algorithm self-describing.
const hashPrefix = "sha256:"

// sha256Prefixed returns the "sha256:"-prefixed hex digest of b — the canonical
// form for every hash value the receipt exposes.
func sha256Prefixed(b []byte) string {
	sum := sha256.Sum256(b)
	return hashPrefix + hex.EncodeToString(sum[:])
}

// computeIgnoreGlobHash returns a stable hash over the sorted ignore and test
// globs, so a receipt reveals when the exclusion set changed between two runs —
// the difference that would otherwise silently shift what got parsed.
func computeIgnoreGlobHash(cfg *config.Config) string {
	globs := make([]string, 0, len(cfg.Ignore)+len(cfg.TestGlobs))
	globs = append(globs, cfg.Ignore...)
	globs = append(globs, cfg.TestGlobs...)
	sort.Strings(globs)
	var sb strings.Builder
	for _, g := range globs {
		sb.WriteString(g)
		sb.WriteByte('\n')
	}
	return sha256Prefixed([]byte(sb.String()))
}

// computeConfigHash returns a stable hash over the effective configuration that
// governs a snapshot — the enabled extractors, explainers, renderers, the ignore
// and test globs, and the output settings. It is a superset of the ignore-glob
// hash: two snapshots with the same config_hash were produced under identical
// analysis settings, so a differing config_hash explains churn that is a config
// change rather than a code change. Each list is sorted so ordering never affects
// the hash.
func computeConfigHash(cfg *config.Config) string {
	var sb strings.Builder
	writeSortedSection := func(label string, vals []string) {
		sb.WriteString(label)
		sb.WriteByte(':')
		cp := append([]string(nil), vals...)
		sort.Strings(cp)
		for _, v := range cp {
			sb.WriteByte('\n')
			sb.WriteString(v)
		}
		sb.WriteByte('\n')
	}
	writeSortedSection("extractors", cfg.Extractors)
	writeSortedSection("explainers", cfg.Explainers)
	writeSortedSection("renderers", cfg.Renderers)
	writeSortedSection("ignore", cfg.Ignore)
	writeSortedSection("test_globs", cfg.TestGlobs)
	fmt.Fprintf(&sb, "output_dir:%s\nmax_context_tokens:%d\nincremental:%t\n",
		cfg.Output.Dir, cfg.Output.MaxContextTokens, cfg.IncrementalEnabled())
	return sha256Prefixed([]byte(sb.String()))
}

// gitInfo captures the repository's VCS state. It returns nil when repoPath is
// not a git working tree or git is unavailable, so a non-git or sandboxed run
// degrades to no git section rather than erroring.
//
// outputDir is the engine's output directory (cfg.Output.Dir), excluded from the
// dirtiness check. enola writes its own artifacts there and --porcelain lists
// untracked paths, so without the exclusion any repo that does not gitignore that
// directory records dirty:true from its first snapshot onward — the cache save
// creates it before this function runs, and it pre-exists every later run. That
// wrong baseline silently disables the git-drift half of the staleness check, whose
// "newly dirty" arm can only fire when the recorded state was clean (see
// freshness.go). The walker already excludes the same directory from analysis, so
// keeping it out here makes the flag describe the SOURCE rather than enola's own
// output. Pass "" to check the whole tree.
func gitInfo(repoPath, outputDir string) *facts.GitInfo {
	commit, err := runGit(repoPath, "rev-parse", "HEAD")
	if err != nil || commit == "" {
		return nil
	}
	info := &facts.GitInfo{Commit: commit}
	if ref, err := runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Ref = ref
	}
	// The repository's identity, as opposed to this revision's. Recorded so a baseline
	// pinned under one checkout path can be recognized as the same repository under
	// another — the property that lets a CI baseline artifact be restored and diffed.
	// Absent origin, no remotes at all, or a detached/bare setup all just leave it empty,
	// matching how the rest of this function degrades.
	if remote, err := runGit(repoPath, "remote", "get-url", "origin"); err == nil {
		info.Remote = facts.NormalizeRemote(remote)
	}
	// --porcelain prints one line per changed/untracked path; any output => dirty.
	// The ':(exclude)' pathspec drops our own output dir; '.' is the positive
	// pathspec it subtracts from. An outputDir configured outside the repo simply
	// matches nothing, which is harmless.
	statusArgs := []string{"status", "--porcelain"}
	if outputDir != "" {
		statusArgs = append(statusArgs, "--", ".", ":(exclude)"+outputDir)
	}
	if status, err := runGit(repoPath, statusArgs...); err == nil && strings.TrimSpace(status) != "" {
		info.Dirty = true
	}
	return info
}

// runGit runs a git subcommand in repoPath and returns its trimmed stdout.
func runGit(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// coverageSummary rolls up the per-service edge_coverage counts the cross-repo
// linker records into a single snapshot-level summary. It returns nil for a
// single-repo snapshot (no service nodes), matching the coverage explainer.
//
// A gap is counted through facts.ClassifyService, so the receipt, coverage_report
// and the coverage explainer cannot drift apart. UnresolvedEdges accumulates for
// every service regardless of class — a partially-covered service still has blind
// spots worth reporting, it just is not a gap.
func coverageSummary(store *facts.Store) *facts.CoverageSummary {
	services := store.ByKind(facts.KindService)
	if len(services) == 0 {
		return nil
	}
	sum := &facts.CoverageSummary{ServicesTotal: len(services)}
	for _, svc := range services {
		detected := readCoverageField(svc, "detected")
		unresolved := readCoverageField(svc, "unresolved")
		sum.UnresolvedEdges += unresolved
		sum.ExternalEdges += readCoverageField(svc, "external")
		if facts.ClassifyService(facts.DependsOnCount(svc), detected, unresolved) == facts.ServiceCoverageGap {
			sum.CoverageGaps++
		}
	}
	return sum
}

// readCoverageField sums one numeric field (e.g. "unresolved" or "external") across
// a service node's edge_coverage entries, tolerating both the in-memory shape and
// the float64 shape that survives a facts.jsonl JSON round-trip (mirrors
// coverage.readCoverage).
func readCoverageField(svc facts.Fact, field string) int {
	if svc.Props == nil {
		return 0
	}
	var raw []map[string]any
	switch v := svc.Props["edge_coverage"].(type) {
	case []map[string]any:
		raw = v
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				raw = append(raw, m)
			}
		}
	default:
		return 0
	}
	total := 0
	for _, m := range raw {
		switch n := m[field].(type) {
		case int:
			total += n
		case float64:
			total += int(n)
		}
	}
	return total
}

// computeSnapshotID returns a stable content fingerprint of "what the graph was
// deterministic over": the byte-stable fact serialization plus the version and
// the effective-config hash that gate what was parsed. It is deliberately NOT
// random — rerunning on the same inputs yields the same ID, so it can key
// equivalence. The result carries the "sha256:" prefix like every receipt hash.
func computeSnapshotID(factsJSONL []byte, enolaVersion, configHash string) string {
	h := sha256.New()
	h.Write(factsJSONL)
	h.Write([]byte(enolaVersion))
	h.Write([]byte(configHash))
	return hashPrefix + hex.EncodeToString(h.Sum(nil))
}

// capParseErrors trims a parse-error slice to the sample cap for the receipt.
func capParseErrors(errs []facts.ParseError) []facts.ParseError {
	if len(errs) <= parseErrorSampleCap {
		return errs
	}
	return errs[:parseErrorSampleCap]
}

// countHeuristicInsights returns how many insights are heuristics (confidence
// below 1.0) rather than structural facts (confidence 1.0). A high heuristic
// share is a signal about how much of the analysis is candidate-grade vs. certain.
func countHeuristicInsights(insights []facts.Insight) int {
	n := 0
	for _, in := range insights {
		if in.Confidence < 1.0 {
			n++
		}
	}
	return n
}
