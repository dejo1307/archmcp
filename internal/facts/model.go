package facts

import (
	"path/filepath"
	"strings"
)

// Fact represents a language-agnostic architectural fact extracted from source code.
type Fact struct {
	Kind      string         `json:"kind"`                // e.g. "module", "symbol", "route", "storage", "dependency"
	Name      string         `json:"name"`                // Canonical name
	File      string         `json:"file,omitempty"`      // Source file (relative to repo root, or repo-prefixed in multi-repo mode)
	Line      int            `json:"line,omitempty"`      // Line number in file
	Repo      string         `json:"repo,omitempty"`      // Repository label (set in multi-repo/append mode)
	Props     map[string]any `json:"props,omitempty"`     // Kind-specific properties
	Relations []Relation     `json:"relations,omitempty"` // Edges to other facts
}

// Relation represents a directed edge between two facts.
type Relation struct {
	Kind   string `json:"kind"`   // e.g. "declares", "imports", "calls", "implements", "depends_on"
	Target string `json:"target"` // Target fact name
}

// Fact kind constants.
const (
	KindModule     = "module"
	KindSymbol     = "symbol"
	KindRoute      = "route"
	KindStorage    = "storage"
	KindDependency = "dependency"
	KindService    = "service" // A whole repository, represented as a node in the cross-repo "graph of graphs".
	// KindTestRef is a reference-only fact emitted from a test/spec file. It carries
	// solely RelCalls relations naming the production symbols the test exercises
	// (Name/File are the test file path). Test files are excluded from normal
	// indexing, so their symbols never become facts; this kind lets the dead-code
	// detector still see that a production symbol is referenced by a test, without
	// any other explainer (which key off symbol/module/route facts) being affected.
	KindTestRef = "test_ref"
	// KindFileRef is a reference-only fact holding call edges made in file-scope
	// (top-level) code — fixtures, initializers, and plugin registration blocks —
	// that have no enclosing symbol to attach to. Like KindTestRef it carries solely
	// RelCalls relations (Name/File are the source file path) and is consumed only by
	// the dead-code detector, so top-level references mark a production symbol used
	// without perturbing the coupling graph or any other explainer.
	KindFileRef = "file_ref"
)

// Relation kind constants.
const (
	RelDeclares     = "declares"
	RelImports      = "imports"
	RelCalls        = "calls"
	RelImplements   = "implements"
	RelDependsOn    = "depends_on"
	RelInstantiates = "instantiates" // Source constructs an instance of target via a constructor call.
	RelInjects      = "injects"      // Source declares target as a DI-injected constructor parameter.
	RelHasMethod    = "has_method"   // Owner type (struct/interface/class) declares target as a method. Synthesized in NewGraph.
)

// Symbol kind property values.
const (
	SymbolFunc      = "function"
	SymbolMethod    = "method"
	SymbolStruct    = "struct"
	SymbolInterface = "interface"
	SymbolType      = "type"
	SymbolClass     = "class"
	SymbolVariable  = "variable"
	SymbolConstant  = "constant"
	SymbolEnum      = "enum"
)

// Module role property values classify a module fact as production code vs.
// non-production (test bundles, build tooling) so downstream analyses (package
// metrics, coverage, insights) can measure the production architecture. Extractors
// set the PropModuleRole prop; consumers treat an absent value as included.
const (
	PropModuleRole       = "module_role"
	ModuleRoleProduction = "production"
	ModuleRoleTest       = "test"
	ModuleRoleTooling    = "tooling"
	ModuleRoleUnknown    = "unknown"
)

// ModuleRoleForPath classifies a module by its directory path segments, for use
// when the extractor has no more authoritative signal (e.g. a leaf-directory
// fallback module covered by no declared target/package): a test-directory segment
// → test, a build-tooling segment → tooling, otherwise unknown. Segment names cover
// the common conventions across languages (Swift Tests/, Ruby spec//bin/, fastlane).
func ModuleRoleForPath(dir string) string {
	for _, seg := range strings.Split(filepath.ToSlash(dir), "/") {
		switch seg {
		case "Tests", "Test", "tests", "test", "spec", "specs",
			"androidTest", "androidUnitTest", "testFixtures":
			return ModuleRoleTest
		case "Scripts", "scripts", "fastlane", "ci_scripts", "bin":
			return ModuleRoleTooling
		}
		// Sub-token match for compound module names (Gradle modules whose purpose
		// is test automation but that compile as a `src/main` source set, e.g.
		// "release-tests", "ui-test-utils", "test-lab"). Split the segment on
		// '-'/'_' and match a token EXACTLY equal to a test word, so genuine
		// single-token names ("latest", "contest", the "abtest" feature) never
		// misfire — they have no separator and stay a single token.
		for _, tok := range strings.FieldsFunc(seg, func(r rune) bool { return r == '-' || r == '_' }) {
			switch tok {
			case "test", "tests", "spec", "specs":
				return ModuleRoleTest
			}
		}
	}
	return ModuleRoleUnknown
}

// Insight represents an architectural insight produced by an explainer.
type Insight struct {
	Title       string     `json:"title"`
	Source      string     `json:"source,omitempty"` // Name of the explainer that produced it (e.g. "unused-routes"). Set centrally by the engine.
	Description string     `json:"description"`
	Confidence  float64    `json:"confidence"` // 0.0 - 1.0
	Evidence    []Evidence `json:"evidence"`
	Actions     []string   `json:"suggested_actions,omitempty"`
}

// Evidence links an insight back to concrete facts/files/symbols.
type Evidence struct {
	File   string `json:"file,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	Fact   string `json:"fact,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Artifact represents a generated output file.
type Artifact struct {
	Name    string `json:"name"` // e.g. "llm_context.md"
	Content []byte `json:"-"`    // Raw content
	Type    string `json:"type"` // MIME type hint
}

// Snapshot holds the complete result of an analysis run.
type Snapshot struct {
	Meta      SnapshotMeta `json:"meta"`
	Facts     []Fact       `json:"facts"`
	Insights  []Insight    `json:"insights"`
	Artifacts []Artifact   `json:"artifacts"`
}

// SnapshotMeta contains metadata about a snapshot generation run. Beyond the
// core provenance fields (repo, time, plugin sets, per-file hashes) it carries
// the "snapshot receipt" fields — a compact, machine-readable record of what the
// deterministic graph was generated over, plus extraction-quality metrics that
// let a consumer (a human, a diff, or an agent improving enola itself) judge how
// complete the extraction was before trusting it.
type SnapshotMeta struct {
	RepoPath     string     `json:"repo_path"`
	GeneratedAt  string     `json:"generated_at"`
	Duration     string     `json:"duration"`
	Extractors   []string   `json:"extractors"`
	Explainers   []string   `json:"explainers"`
	Renderers    []string   `json:"renderers"`
	FileHashes   []FileHash `json:"file_hashes,omitempty"`
	FactCount    int        `json:"fact_count"`
	InsightCount int        `json:"insight_count"`

	// Receipt / provenance fields. Hash values carry a "sha256:" prefix.
	EnolaVersion string   `json:"enola_version,omitempty"` // build version that produced this snapshot
	SnapshotID   string   `json:"snapshot_id,omitempty"`   // content fingerprint (see engine.computeSnapshotID); stable across reruns on the same inputs
	Git          *GitInfo `json:"git,omitempty"`           // repo VCS state, nil when not a git repo
	ConfigHash   string   `json:"config_hash,omitempty"`   // hash of the effective config (extractors, explainers, renderers, globs, output) — a superset of IgnoreGlobHash

	// Extraction-quality fields (the loop signal).
	FilesSeen         int               `json:"files_seen,omitempty"`         // source files the walker enumerated (excludes ignored)
	FilesParsed       int               `json:"files_parsed,omitempty"`       // distinct files that produced at least one fact
	FilesSkipped      int               `json:"files_skipped,omitempty"`      // paths dropped by ignore globs
	SkippedSample     []string          `json:"skipped_sample,omitempty"`     // a capped sample of skipped paths
	IgnoreGlobHash    string            `json:"ignore_glob_hash,omitempty"`   // hash of the sorted ignore+test globs
	ParseErrors       int               `json:"parse_errors,omitempty"`       // count of extractor detect/parse failures (non-fatal)
	ParseErrorSample  []ParseError      `json:"parse_error_sample,omitempty"` // a capped sample of those failures
	HeuristicInsights int               `json:"heuristic_insights,omitempty"` // count of insights with confidence < 1.0 (heuristics, vs. structural facts)
	Coverage          *CoverageSummary  `json:"coverage,omitempty"`           // cross-repo edge-coverage rollup, nil in single-repo mode
	OutputHashes      map[string]string `json:"output_hashes,omitempty"`      // artifact name -> "sha256:"-prefixed hash of its written bytes
}

// FileHash tracks a file's content hash for incremental updates.
type FileHash struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	ModTime string `json:"mod_time"`
}

// GitInfo records the VCS state of the repository at snapshot time, so a receipt
// pins the graph to an exact commit and flags an uncommitted (dirty) tree.
type GitInfo struct {
	Ref    string `json:"ref,omitempty"`    // branch or symbolic ref (e.g. "main")
	Commit string `json:"commit,omitempty"` // full commit SHA of HEAD
	Dirty  bool   `json:"dirty"`            // true when the working tree has uncommitted changes
}

// ParseError records a non-fatal extraction failure — an extractor that could
// not detect or parse its inputs. These are counted rather than swallowed so a
// receipt can surface thin extraction.
type ParseError struct {
	Extractor string `json:"extractor"`
	File      string `json:"file,omitempty"` // set when the failure is attributable to a specific file
	Msg       string `json:"msg"`
}

// CoverageSummary is a snapshot-level rollup of the cross-repo edge coverage the
// linker records per service, so a receipt reflects unresolved outbound edges
// without the consumer running coverage_report.
type CoverageSummary struct {
	ServicesTotal   int `json:"services_total"`
	CoverageGaps    int `json:"coverage_gaps"`    // services with unresolved outbound call sites
	UnresolvedEdges int `json:"unresolved_edges"` // total detected-but-unresolved outbound edges
}

// Receipt is the compact, machine-readable manifest written to receipt.json — a
// projection of SnapshotMeta that proves what the deterministic graph was
// generated over (version, git, id, plugin sets, ignore-glob hash, output
// hashes) and carries the extraction-quality metrics a consumer or an agent
// improving enola can act on. It deliberately omits the large per-file FileHashes
// list that lives in snapshot.meta.json (the internal superset).
type Receipt struct {
	SnapshotID     string            `json:"snapshot_id"`
	EnolaVersion   string            `json:"enola_version"`
	GeneratedAt    string            `json:"generated_at"`
	Duration       string            `json:"duration"`
	RepoPath       string            `json:"repo_path"`
	Git            *GitInfo          `json:"git,omitempty"`
	Extractors     []string          `json:"extractors"`
	Explainers     []string          `json:"explainers"`
	Renderers      []string          `json:"renderers,omitempty"`
	ConfigHash     string            `json:"config_hash,omitempty"`
	IgnoreGlobHash string            `json:"ignore_glob_hash,omitempty"`
	OutputHashes   map[string]string `json:"output_hashes,omitempty"`

	FactCount    int            `json:"fact_count"`
	InsightCount int            `json:"insight_count"`
	Quality      ReceiptQuality `json:"quality"`
}

// ReceiptQuality groups the extraction-completeness metrics — the loop signal a
// consumer polls to detect thin extraction.
type ReceiptQuality struct {
	FilesSeen         int              `json:"files_seen"`
	FilesParsed       int              `json:"files_parsed"`
	FilesSkipped      int              `json:"files_skipped"`
	SkippedSample     []string         `json:"skipped_sample,omitempty"`
	ParseErrors       int              `json:"parse_errors"`
	ParseErrorSample  []ParseError     `json:"parse_error_sample,omitempty"`
	HeuristicInsights int              `json:"heuristic_insights"`
	Coverage          *CoverageSummary `json:"coverage,omitempty"`
}

// Receipt projects a SnapshotMeta into the compact receipt manifest.
func (m SnapshotMeta) Receipt() Receipt {
	return Receipt{
		SnapshotID:     m.SnapshotID,
		EnolaVersion:   m.EnolaVersion,
		GeneratedAt:    m.GeneratedAt,
		Duration:       m.Duration,
		RepoPath:       m.RepoPath,
		Git:            m.Git,
		Extractors:     m.Extractors,
		Explainers:     m.Explainers,
		Renderers:      m.Renderers,
		ConfigHash:     m.ConfigHash,
		IgnoreGlobHash: m.IgnoreGlobHash,
		OutputHashes:   m.OutputHashes,
		FactCount:      m.FactCount,
		InsightCount:   m.InsightCount,
		Quality: ReceiptQuality{
			FilesSeen:         m.FilesSeen,
			FilesParsed:       m.FilesParsed,
			FilesSkipped:      m.FilesSkipped,
			SkippedSample:     m.SkippedSample,
			ParseErrors:       m.ParseErrors,
			ParseErrorSample:  m.ParseErrorSample,
			HeuristicInsights: m.HeuristicInsights,
			Coverage:          m.Coverage,
		},
	}
}
