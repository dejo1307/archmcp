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
	// KindIntent is a DECLARED fact: one entry of a repo's architectural intent
	// (a consumed seam, a served surface, a layer, a service identity), compiled
	// into the store from enola-intent.yaml or a cluster config's intent block.
	// Every other kind is measured from source; this one states what the source
	// is SUPPOSED to do, and the intent explainer verdicts the two against each
	// other. Carries intent_kind plus provenance (source, overridden).
	KindIntent = "intent"
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

// Cross-repo dependency-fact "type" prop values. Both the linker that writes these
// facts and every reader (explainers, renderers, the MCP server) key off them, so they
// live here rather than being duplicated as literals on each side.
const (
	// TypeCrossRepo marks a real, DIRECTIONAL cross-repo edge: one repo imports or
	// calls the other. Only these attach a depends_on relation to the consumer's
	// service node, so only these are traversable.
	TypeCrossRepo = "cross_repo"
	// TypeCrossRepoSharedCode marks a SYMMETRIC shared-code coupling: two repos declare
	// many of the same distinctive type names but neither imports or calls the other.
	// It is queryable evidence with no relation attached, so it never appears in
	// traverse/find_path/impact_analysis — shared code is not a dependency, and unlike
	// a dependency it does not compose across hops.
	TypeCrossRepoSharedCode = "cross_repo_shared_code"
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
	RelHandledBy    = "handled_by"   // A route/endpoint is served by target (e.g. a gRPC RPC route → its Go handler method). Added post-extraction.
)

// StorageKindTopic is the storage_kind prop value for a KindStorage fact that
// represents a messaging topic reference (e.g. a Kafka topic a service produces to
// or consumes from), as opposed to a database table or object store. The cross-repo
// async linker keys producer/consumer binding on it. The topic name is the fact's
// Name; the messaging_role prop (MessagingRole*) records the side when known, and is
// otherwise inferred by the linker from topic-name ownership.
const StorageKindTopic = "topic"

// Messaging role property values (the messaging_role prop) for a topic storage fact:
// which side of a topic a reference represents. Left unset when only a bare topic
// reference is seen, in which case the linker infers direction from the topic name's
// owning-service prefix.
const (
	MessagingRoleProducer = "producer"
	MessagingRoleConsumer = "consumer"
)

// MethodAny is the HTTP-method value for a server route that handles every verb
// rather than one specific method — a raw servlet (doGet/doPost/…) or a mapping
// declared without a verb. The cross-repo HTTP linker treats it as a wildcard that
// a client call of any concrete method matches. Extractors that cannot attribute a
// route to a single verb emit this instead of guessing one (which would create a
// spurious method mismatch) or omitting the method (which drops the route from
// matching entirely).
const MethodAny = "*"

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

// Coupling-kind property values annotate a synthetic module-coupling dependency
// fact (currently emitted by the Ruby extractor) with the reference that produced
// the edge, so consumers can treat edge classes differently — e.g. the cycles
// explainer excludes CouplingAssociation, because ActiveRecord has_many/belongs_to
// pairs are bidirectional by nature and would manufacture false cycles. Set on the
// Props["coupling_kind"] of a synthetic edge; an absent value means unclassified
// (treated as a normal edge). When one edge arises from several references, the
// hardest kind wins (a real reference is never downgraded to an association).
const (
	PropCouplingKind = "coupling_kind"

	CouplingReference   = "reference"   // constant-receiver method call
	CouplingInheritance = "inheritance" // superclass
	CouplingMixin       = "mixin"       // include/extend/prepend
	CouplingAssociation = "association" // ActiveRecord has_many/belongs_to/...
	CouplingRequire     = "require"     // require / require_relative
	CouplingPackwerk    = "packwerk"    // explicit package.yml dependency
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

	// Informational marks a finding that DESCRIBES the graph rather than complaining
	// about it — "Architecture pattern: declared (enola)", "Intent override: the cluster
	// config replaced this repo's declaration". They are worth reporting and must never
	// be gradeable, and the distinction is not expressible in confidence: both are exact,
	// which is precisely the problem. A repository that declares a layer order for the
	// first time emits a new `layers` finding at 1.00 describing the declaration, and
	// under --fail-on=layers that would fail the very pull request that adopted the
	// policy. See check.Policy.fails.
	Informational bool `json:"informational,omitempty"`
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
	SnapshotID   string   `json:"snapshot_id,omitempty"`   // content fingerprint (see engine.newSnapshotIDHasher); stable across reruns on the same inputs
	Git          *GitInfo `json:"git,omitempty"`           // repo VCS state, nil when not a git repo
	ConfigHash   string   `json:"config_hash,omitempty"`   // hash of the effective config (extractors, explainers, renderers, globs, output) — a superset of IgnoreGlobHash

	// ExtractorVersion identifies the EXTRACTION BEHAVIOUR that produced this graph:
	// internal/engine.cacheVersion, the constant bumped whenever an extractor starts
	// reading something differently (and guarded by internal/cachecov, which refuses a
	// bump without a covering test).
	//
	// It is provenance EnolaVersion cannot supply. A released binary carries a version
	// that moves with every release, so an upgrade is visible; a local build carries the
	// constant "dev" forever, so an extractor change is invisible in it. Every field
	// beside this one then reads identical across a change that rewrote the graph — which
	// is exactly how a fix that removed 21 fabricated facts came to be recorded, in this
	// repository's own architecture history, as somebody deleting 21 things from the
	// codebase.
	//
	// The alternative — identifying the binary itself by size and mtime, as the cache does
	// in buildIdentity — catches undeclared changes too, and was rejected: it would mark
	// every recompile as a new extraction regime, which for anyone developing enola is
	// several times an hour, and a marker that fires constantly stops carrying meaning.
	ExtractorVersion string `json:"extractor_version,omitempty"`

	// Extraction-quality fields (the loop signal).
	FilesSeen      int      `json:"files_seen,omitempty"`       // source files the walker enumerated (excludes ignored)
	FilesParsed    int      `json:"files_parsed,omitempty"`     // distinct files that produced at least one fact
	SourceBytes    int64    `json:"source_bytes,omitempty"`     // on-disk bytes of the FilesParsed set — the corpus this graph replaces reading (see pkg/status value model)
	FilesSkipped   int      `json:"files_skipped,omitempty"`    // ignored FILES the walker visited; a pruned directory counts once in DirsSkipped, not once per file
	DirsSkipped    int      `json:"dirs_skipped,omitempty"`     // ignored DIRECTORIES pruned whole; their contents are never visited, so they are counted nowhere else
	SkippedSample  []string `json:"skipped_sample,omitempty"`   // a capped sample of both, each naming the glob that matched it
	IgnoreGlobHash string   `json:"ignore_glob_hash,omitempty"` // hash of the sorted ignore+test globs
	// ShadowedExtractors names extractors that detected this repository but were
	// excluded by an explicit `extractors:` list — languages present in the source
	// and absent from the graph. Recorded because the alternative evidence is a
	// negative (an extractor that never appears in the log), which is unreadable
	// after the fact: a receipt showing thin extraction should say whose facts are
	// missing by configuration rather than by absence.
	ShadowedExtractors []string          `json:"shadowed_extractors,omitempty"`
	ParseErrors        int               `json:"parse_errors,omitempty"`       // count of extractor detect/parse failures (non-fatal)
	ParseErrorSample   []ParseError      `json:"parse_error_sample,omitempty"` // a capped sample of those failures
	HeuristicInsights  int               `json:"heuristic_insights,omitempty"` // count of insights with confidence < 1.0 (heuristics, vs. structural facts)
	Coverage           *CoverageSummary  `json:"coverage,omitempty"`           // cross-repo edge-coverage rollup, nil in single-repo mode
	OutputHashes       map[string]string `json:"output_hashes,omitempty"`      // artifact name -> "sha256:"-prefixed hash of its written bytes
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

	// Remote is the origin URL, normalized to a comparable identity
	// ("github.com/org/repo" — no scheme, credentials, port or ".git" suffix). Empty
	// when there is no remote, no git, or no origin.
	//
	// It exists so two snapshots of the SAME repository taken at different absolute
	// paths — a CI runner's checkout and a developer's working copy — can be recognized
	// as comparable. Commit identifies a revision; this identifies the repository.
	// A remote is used rather than the root commit because the root commit is
	// unreachable in a shallow clone, which is what CI checkouts default to.
	Remote string `json:"remote,omitempty"`
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
	CoverageGaps    int `json:"coverage_gaps"`            // services classified ServiceCoverageGap: no resolved outbound edge, yet unresolved call sites were detected. A service that resolved some edges is partially covered, not a gap
	UnresolvedEdges int `json:"unresolved_edges"`         // detected outbound edges that did not resolve to a loaded service (internal blind spots; excludes external)
	ExternalEdges   int `json:"external_edges,omitempty"` // detected outbound edges to hardcoded external hosts (third-party APIs) — expected, not a blind spot
}

// Receipt is the compact, machine-readable manifest written to receipt.json — a
// projection of SnapshotMeta that proves what the deterministic graph was
// generated over (version, git, id, plugin sets, ignore-glob hash, output
// hashes) and carries the extraction-quality metrics a consumer or an agent
// improving enola can act on. It deliberately omits the large per-file FileHashes
// list that lives in snapshot.meta.json (the internal superset).
type Receipt struct {
	SnapshotID string `json:"snapshot_id"`
	// EnolaVersion is the build; ExtractorVersion is what that build EXTRACTS LIKE. They
	// differ for every local build, where the former is the constant "dev". See
	// SnapshotMeta.ExtractorVersion.
	EnolaVersion     string            `json:"enola_version"`
	ExtractorVersion string            `json:"extractor_version,omitempty"`
	GeneratedAt      string            `json:"generated_at"`
	Duration         string            `json:"duration"`
	RepoPath         string            `json:"repo_path"`
	Git              *GitInfo          `json:"git,omitempty"`
	Extractors       []string          `json:"extractors"`
	Explainers       []string          `json:"explainers"`
	Renderers        []string          `json:"renderers,omitempty"`
	ConfigHash       string            `json:"config_hash,omitempty"`
	IgnoreGlobHash   string            `json:"ignore_glob_hash,omitempty"`
	OutputHashes     map[string]string `json:"output_hashes,omitempty"`

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
	DirsSkipped       int              `json:"dirs_skipped"`
	SkippedSample     []string         `json:"skipped_sample,omitempty"`
	ParseErrors       int              `json:"parse_errors"`
	ParseErrorSample  []ParseError     `json:"parse_error_sample,omitempty"`
	HeuristicInsights int              `json:"heuristic_insights"`
	Coverage          *CoverageSummary `json:"coverage,omitempty"`
}

// Receipt projects a SnapshotMeta into the compact receipt manifest.
func (m SnapshotMeta) Receipt() Receipt {
	return Receipt{
		SnapshotID:       m.SnapshotID,
		EnolaVersion:     m.EnolaVersion,
		ExtractorVersion: m.ExtractorVersion,
		GeneratedAt:      m.GeneratedAt,
		Duration:         m.Duration,
		RepoPath:         m.RepoPath,
		Git:              m.Git,
		Extractors:       m.Extractors,
		Explainers:       m.Explainers,
		Renderers:        m.Renderers,
		ConfigHash:       m.ConfigHash,
		IgnoreGlobHash:   m.IgnoreGlobHash,
		OutputHashes:     m.OutputHashes,
		FactCount:        m.FactCount,
		InsightCount:     m.InsightCount,
		Quality: ReceiptQuality{
			FilesSeen:         m.FilesSeen,
			FilesParsed:       m.FilesParsed,
			FilesSkipped:      m.FilesSkipped,
			DirsSkipped:       m.DirsSkipped,
			SkippedSample:     m.SkippedSample,
			ParseErrors:       m.ParseErrors,
			ParseErrorSample:  m.ParseErrorSample,
			HeuristicInsights: m.HeuristicInsights,
			Coverage:          m.Coverage,
		},
	}
}

// GraphReceipt is the graph-wide manifest written to ~/.enola/receipt.json — it
// describes the CURRENT multi-repo "graph of graphs": which repositories compose
// it, the git commit each sits on, when each entered the graph and how long it has
// been a member, and what the graph consists of now. Unlike the per-repo Receipt
// (which proves what one deterministic snapshot was generated over), this is a
// machine-global picture of the live graph. It works for a single-repo graph too
// (Repos then has one entry).
type GraphReceipt struct {
	GeneratedAt        string           `json:"generated_at"`          // RFC3339 UTC, this write
	EnolaVersion       string           `json:"enola_version"`         // build version that produced the current graph
	SnapshotID         string           `json:"snapshot_id"`           // whole-graph content fingerprint (SnapshotMeta.SnapshotID)
	FactCount          int              `json:"fact_count"`            // total facts across all repos
	InsightCount       int              `json:"insight_count"`         // total insights
	ServiceCount       int              `json:"service_count"`         // KindService nodes = repos materialized in the cross-repo graph
	CrossRepoEdgeCount int              `json:"cross_repo_edge_count"` // consumer->provider edges in the cross-repo "graph of graphs" (NOT the total dependency-fact count, which also covers ordinary imports)
	Coverage           *CoverageSummary `json:"coverage,omitempty"`    // cross-repo edge-coverage rollup, nil in single-repo mode
	Repos              []GraphRepoEntry `json:"repos"`                 // one entry per repository in the graph, sorted by Label
}

// GraphRepoEntry describes one repository's membership in the current graph.
type GraphRepoEntry struct {
	Label           string   `json:"label"`                       // filepath.Base(absRepo); the store's repo label
	Path            string   `json:"path"`                        // absolute repo root
	Git             *GitInfo `json:"git,omitempty"`               // ref/commit/dirty; nil for non-git dirs
	AddedAt         string   `json:"added_at"`                    // RFC3339 UTC, first time this label entered the graph (merged forward across regenerations)
	InGraphFor      string   `json:"in_graph_for"`                // human duration since AddedAt (e.g. "72h3m0s"); derived, recomputed each write
	FactCount       int      `json:"fact_count"`                  // facts tagged with this repo label
	CommitChangedAt string   `json:"commit_changed_at,omitempty"` // RFC3339 UTC, last time the recorded commit moved; AddedAt is NOT reset when the commit changes

	// SourceBytes is the on-disk size of this repo's parsed source — the corpus
	// figure the value model prices against (see pkg/status). It is carried here
	// so a server restarting onto a restored graph knows how large that graph is
	// without re-snapshotting: the facts come back from disk, and so must the
	// measurement of what they were extracted from.
	//
	// Zero means "not recorded" — a receipt written before this field existed, or
	// a repo whose snapshot metadata could not be read. Consumers treat zero as
	// unknown rather than as an empty corpus.
	SourceBytes int64 `json:"source_bytes,omitempty"`
}
