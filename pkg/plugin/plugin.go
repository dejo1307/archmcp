package plugin

import (
	"context"

	"github.com/enola-labs/enola/internal/facts"
)

// Extractor parses source files for a specific language and emits architectural facts.
type Extractor interface {
	// Name returns the extractor identifier (e.g. "go", "typescript").
	Name() string
	// Detect returns true if this extractor supports the given repository.
	Detect(repoPath string) (bool, error)
	// Extract parses files in the repository and returns extracted facts.
	Extract(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error)
}

// FileOwner is an optional interface an Extractor may implement to declare which
// files it parses. The engine uses it to scope incremental caching: an
// extractor's previously computed facts are reused only when the contents of the
// files it owns (and the repo's shared config/manifest files) are unchanged.
// Extractors that do not implement it are never cached and always re-run.
type FileOwner interface {
	// OwnsFile reports whether this extractor parses the given repo-relative
	// file. It must be a pure function of the path (a superset is safe — owning a
	// file the extractor ignores only narrows what counts as "shared config").
	OwnsFile(relFile string) bool
}

// KeyDependent is an optional interface a FileOwner Extractor may implement to
// widen its incremental-cache key beyond the files it parses. The engine folds
// any file for which AffectsKey returns true into the extractor's cache key, so a
// change to a file that influences the extractor's output — without being one it
// owns — still invalidates its cache. The JVM extractors use this: their
// multi-module import resolution reads the package declarations of the OTHER JVM
// language's files (Kotlin resolves against .java packages and vice versa), so a
// cross-language package change must re-run the extractor.
type KeyDependent interface {
	// AffectsKey reports whether the given repo-relative file influences this
	// extractor's output even though OwnsFile returns false for it. It must be a
	// pure function of the path; a superset is safe (over-inclusion only reduces
	// cache reuse, never soundness).
	AffectsKey(relFile string) bool
}

// TestRefExtractor is an optional interface an Extractor may implement to parse
// test/spec files for their outbound references into production code only. The
// engine calls it with the test files (matched by config.TestGlobs) that the
// extractor owns. Implementations must emit ONLY reference facts (facts.KindTestRef
// carrying RelCalls relations) and no symbol/module/route facts, so test code
// never becomes a dead-code candidate and no other explainer is affected.
// Extractors that do not implement it are simply skipped for test-ref extraction.
type TestRefExtractor interface {
	// ExtractTestRefs parses the repo-relative test files in testFiles and returns
	// reference-only facts (facts.KindTestRef).
	//
	// prodFiles is the production file list the same snapshot passed to Extract —
	// every non-test file, across all languages. It exists because resolving a test's
	// reference can require knowing which production modules are real: Python spells
	// an absolute-import call target as a dotted path ("pkg.mod.func") and can only
	// rewrite it to a canonical symbol name by checking that path against the set of
	// files that exist. Without it, every such target looks external and is dropped,
	// and the pass emits facts with no usable edges.
	//
	// Implementations that resolve purely lexically (Go via import paths, Ruby and
	// TypeScript via their own conventions) ignore it. It is passed rather than
	// re-derived inside the extractor deliberately: only the engine knows which files
	// survived the ignore globs, and a second walk would duplicate that knowledge —
	// the mistake that let fixture directories into the graph via the self-walking
	// OpenAPI and gRPC extractors.
	ExtractTestRefs(ctx context.Context, repoPath string, testFiles, prodFiles []string) ([]facts.Fact, error)
}

// Explainer analyzes facts and produces architectural insights.
type Explainer interface {
	// Name returns the explainer identifier (e.g. "cycles", "layers").
	Name() string
	// Explain analyzes the fact store and returns insights.
	Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error)
}

// Renderer produces output artifacts from a snapshot.
type Renderer interface {
	// Name returns the renderer identifier (e.g. "llm_context").
	Name() string
	// Render produces artifacts from the given snapshot.
	Render(ctx context.Context, snapshot *facts.Snapshot) ([]facts.Artifact, error)
}
