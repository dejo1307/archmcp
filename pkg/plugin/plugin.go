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

// TestRefExtractor is an optional interface an Extractor may implement to parse
// test/spec files for their outbound references into production code only. The
// engine calls it with the test files (matched by config.TestGlobs) that the
// extractor owns. Implementations must emit ONLY reference facts (facts.KindTestRef
// carrying RelCalls relations) and no symbol/module/route facts, so test code
// never becomes a dead-code candidate and no other explainer is affected.
// Extractors that do not implement it are simply skipped for test-ref extraction.
type TestRefExtractor interface {
	// ExtractTestRefs parses the given repo-relative test files and returns
	// reference-only facts (facts.KindTestRef).
	ExtractTestRefs(ctx context.Context, repoPath string, files []string) ([]facts.Fact, error)
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
