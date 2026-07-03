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

// SnapshotMeta contains metadata about a snapshot generation run.
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
}

// FileHash tracks a file's content hash for incremental updates.
type FileHash struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	ModTime string `json:"mod_time"`
}
