package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Appending a repository the union already holds replaces its slice. Without
// this, a second append of the same repo doubled every one of its facts, so a
// union could only ever be rebuilt from the first repository up.
func TestAppend_ReplacesARepositoryAlreadyInTheUnion(t *testing.T) {
	eng := freezeTestEngine(t)
	repoA := freezeTestRepo(t)
	repoB := freezeTestRepo(t)

	if _, err := eng.GenerateSnapshot(context.Background(), repoA, false); err != nil {
		t.Fatalf("generate A: %v", err)
	}
	if _, err := eng.GenerateSnapshot(context.Background(), repoB, true); err != nil {
		t.Fatalf("append B: %v", err)
	}
	labelA, labelB := filepath.Base(repoA), filepath.Base(repoB)
	before := eng.Store().CountByRepo(labelB)
	beforeA := eng.Store().CountByRepo(labelA)
	if before == 0 || beforeA == 0 {
		t.Fatalf("precondition: both repos must contribute facts (A=%d, B=%d)", beforeA, before)
	}

	// Change B so its second reading differs from its first, then append it again.
	extra := filepath.Join(repoB, "extra.go")
	if err := os.WriteFile(extra, []byte("package main\n\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.GenerateSnapshot(context.Background(), repoB, true); err != nil {
		t.Fatalf("re-append B: %v", err)
	}

	after := eng.Store().CountByRepo(labelB)
	if after >= 2*before {
		t.Fatalf("re-appending %s doubled its facts: %d before, %d after", labelB, before, after)
	}
	if after < before {
		t.Fatalf("re-appending %s lost facts: %d before, %d after", labelB, before, after)
	}
	found := false
	for _, f := range eng.Store().ByRepo(labelB) {
		if f.Name != "" && filepath.Base(f.File) == "extra.go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the re-read of %s did not carry the file added between readings", labelB)
	}
	if got := eng.Store().CountByRepo(labelA); got != beforeA {
		t.Fatalf("re-appending %s changed %s's facts: %d -> %d", labelB, labelA, beforeA, got)
	}
}
