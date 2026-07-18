package engine_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/bootstrap"
)

// TestEngineConcurrentGenerateAndRead exercises the shared engine under the same
// concurrency the MCP go-sdk produces (tool handlers dispatched on their own
// goroutines, unserialized). It hammers a single *engine.Engine with overlapping
// generate_snapshot writers and lock-free reader tools and must be race-clean under
// `go test -race`.
//
// It proves the atomic snapshot-swap: readers never observe a torn/half-built store,
// the `snapshot`/`repoPaths` fields never race a concurrent regeneration, and
// snapshot.Facts (which aliases the store's backing slice) is safe to iterate while
// another repo is being regenerated because a published store is never mutated again.
func TestEngineConcurrentGenerateAndRead(t *testing.T) {
	repoA := copyTree(t, "testdata/repos/go_sample", t.TempDir())
	repoB := copyTree(t, "testdata/repos/python_sample", t.TempDir())

	eng, _, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: "no-such-config.yaml", // falls back to defaults
	})
	if err != nil {
		t.Fatalf("bootstrap.NewEngine: %v", err)
	}
	// Don't persist the extractor cache: keeps the fixtures' .enola dirs untouched and
	// avoids on-disk cache churn between the rapid regenerations below.
	eng.SetPersistCache(false)

	ctx := context.Background()

	// Seed an initial published snapshot so readers have something from the start.
	if _, err := eng.GenerateSnapshot(ctx, repoA, false); err != nil {
		t.Fatalf("initial GenerateSnapshot: %v", err)
	}

	const duration = 2 * time.Second
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	fail := func(format string, args ...any) {
		select {
		case errCh <- fmt.Errorf(format, args...):
		default:
		}
	}

	// Writers: overlapping regenerations. e.mu serializes generate-vs-generate, but
	// these run concurrently with all readers. Alternate a fresh single-repo snapshot
	// of A with an append of B, so repoPaths and the store both grow and reset.
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				appendMode := (i+id)%2 == 1
				repo := repoA
				if appendMode {
					repo = repoB
				}
				if _, err := eng.GenerateSnapshot(ctx, repo, appendMode); err != nil {
					fail("writer %d: GenerateSnapshot(append=%v): %v", id, appendMode, err)
					return
				}
			}
		}(w)
	}

	// Readers: lock-free accessors, plus the FactsRef-aliasing hot path.
	for r := 0; r < 12; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				st := eng.Store()
				_ = st.Count()
				_ = st.ByKind(facts.KindSymbol)
				_ = st.Query("", "", "", "")

				// The crux: a published snapshot's FactCount must always equal the
				// length of its aliased Facts slice. A torn publish (or a store
				// mutated out from under the alias) would break this invariant or
				// trip the race detector.
				if snap := eng.Snapshot(); snap != nil {
					n := 0
					for range snap.Facts {
						n++
					}
					if snap.Meta.FactCount != n {
						fail("torn snapshot: FactCount=%d but len(Facts)=%d", snap.Meta.FactCount, n)
						return
					}
					if len(snap.Facts) > 0 {
						f := snap.Facts[0]
						_ = eng.ResolveFactFile(&f)
					}
				}
				_ = eng.RepoPaths()
			}
		}()
	}

	// Artifact reader: exercises WriteArtifacts's sibling in-memory read path (bundle
	// load + store/snapshot serialization) concurrently with regeneration.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			if _, err := eng.GetArtifact("facts.jsonl"); err != nil {
				fail("GetArtifact(facts.jsonl): %v", err)
				return
			}
			if _, err := eng.GetArtifact("receipt.json"); err != nil {
				fail("GetArtifact(receipt.json): %v", err)
				return
			}
		}
	}()

	// A single WriteArtifacts driver (sequential to itself, so no on-disk write race)
	// running concurrently with generates, to cover the immutable-republish CAS path.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			if err := eng.WriteArtifacts(repoA); err != nil {
				fail("WriteArtifacts: %v", err)
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
