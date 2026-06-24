// Package parallel provides small concurrency helpers used by the extractors to
// parse files in parallel while keeping output deterministic.
package parallel

import (
	"context"
	"runtime"
	"sync"
)

// MapFiles applies fn to each item concurrently, bounded by GOMAXPROCS workers,
// and returns the results in the SAME order as items (results[i] == fn(items[i])).
//
// Output order is independent of completion order, so callers that concatenate
// the per-item results get byte-for-byte deterministic output regardless of how
// the work was scheduled — which is essential for enola's reproducibility
// guarantee. If ctx is cancelled mid-run, items not yet started are skipped and
// their slots hold the zero value of T.
func MapFiles[T any](ctx context.Context, items []string, fn func(item string) T) []T {
	results := make([]T, len(items))
	if len(items) == 0 {
		return results
	}

	workers := runtime.GOMAXPROCS(0)
	if workers > len(items) {
		workers = len(items)
	}

	idxCh := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := range idxCh {
				// Honor cancellation, but keep draining idxCh so the producer
				// below never blocks on a full send.
				select {
				case <-ctx.Done():
					continue
				default:
				}
				results[i] = fn(items[i])
			}
		}()
	}

	for i := range items {
		idxCh <- i
	}
	close(idxCh)
	wg.Wait()

	return results
}
