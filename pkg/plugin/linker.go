package plugin

import (
	"context"

	"github.com/enola-labs/enola/internal/facts"
)

// BindStage is when a Binder runs relative to cross-repo linking. It is a declared
// property of the binder rather than an argument the engine passes, because the
// ordering it encodes is a correctness constraint, not a scheduling preference:
// a binder that rewrites route identities MUST run before the linker matches on
// them, and one that reads the linker's verdicts MUST run after. Both used to be
// enforced only by the order of five calls in a function body, where nothing
// recorded which of them mattered.
type BindStage string

const (
	// StagePreLink runs before cross-repo linking. Use it for a binder that
	// changes what the linker will MATCH ON — a provisional route name resolved to
	// its true wire path, say. Running such a binder after the linker would leave
	// the link computed against the un-rewritten value.
	StagePreLink BindStage = "pre-link"
	// StagePostLink runs after cross-repo linking, before the graph index is built.
	// Use it for a binder that reads the linker's output, or that only resolves
	// references WITHIN a repo and so has no ordering relationship to linking at
	// all. When in doubt this is the right stage: it is the more constrained one.
	StagePostLink BindStage = "post-link"
)

// Binder resolves references across an assembled fact set that no single extractor
// could resolve alone.
//
// An extractor sees one file at a time, in one language. Some edges are only
// discoverable once everything is in one store: a gRPC route read from a .proto and
// the method implementing it in Go or Python are extracted by different extractors
// that never see each other's output, and the convention tying them together
// (protoc's generated base type) is visible only in the assembled graph. That is the
// work a Binder does — and why it is a distinct plugin kind from an Extractor, which
// cannot see across files, and an Explainer, which may not modify facts.
//
// CONTRACT. Bind runs after extraction, in the stage the binder declares, before the
// graph index is built. Implementations:
//
//   - may only ADD OR UPDATE relations and props on facts already in the store.
//     Adding or removing FACTS would make the graph depend on which binders were
//     enabled, and two snapshots taken with different sets would no longer be
//     comparable — the same rule Annotator carries, one step wider because a Binder
//     may also add relations. (The one binder that rewrites a fact's Name does so via
//     remove-and-re-add to keep the store's name index consistent; that is a rewrite
//     of an existing fact, not a new one.)
//   - MUST be idempotent. Every binder re-runs on every snapshot and every append, so
//     a second run over its own output must be a no-op — check for the relation
//     before appending it.
//   - MUST be deterministic, and must not depend on the order binders were
//     registered in. Two binders in the same stage may run in any order, so a binder
//     that would behave differently depending on whether another has already run is
//     using the wrong stage, or the wrong gate.
//
// Determinism is the load-bearing one: facts.jsonl is hashed into the snapshot ID, so
// a binder whose output depends on registration order would make an unchanged tree
// produce a different snapshot from run to run.
type Binder interface {
	// Name identifies the binder (e.g. "grpc-impl", "http-handler").
	Name() string
	// Stage reports when this binder runs relative to cross-repo linking.
	Stage() BindStage
	// Bind resolves references over the assembled store, in place.
	Bind(ctx context.Context, store *facts.Store) error
}
