package coverage_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/engine"
	"github.com/enola-labs/enola/internal/extractors/goextractor"
	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/coverage"
)

// exampleDir is the published demonstration under examples/cross-repo.
const exampleDir = "../../examples/cross-repo"

// TestPublishedExample_StillDemonstratesWhatItClaims runs the example a reader is told
// to run, and asserts the two things its README promises.
//
// A worked example is a claim like any other, and an unexercised one rots quietly: an
// extractor change could stop composing the prefix, the example would keep running, and
// the README would go on describing an outcome that no longer happens. That is worse
// than having no example, because a reader checks it precisely when deciding whether to
// trust the tool.
func TestPublishedExample_StillDemonstratesWhatItClaims(t *testing.T) {
	if _, err := os.Stat(exampleDir); err != nil {
		t.Skipf("example not present: %v", err)
	}
	apiDir, err := filepath.Abs(filepath.Join(exampleDir, "api"))
	if err != nil {
		t.Fatal(err)
	}
	webDir, err := filepath.Abs(filepath.Join(exampleDir, "web"))
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eng.RegisterExtractor(goextractor.New())
	eng.SetPersistCache(false) // never write into the published example

	ctx := context.Background()
	// Append mode on the second repo is what makes this one graph; the engine runs the
	// cross-repo linker itself as part of that.
	for i, dir := range []string{apiDir, webDir} {
		if _, err := eng.GenerateSnapshot(ctx, dir, i > 0); err != nil {
			t.Fatalf("indexing %s: %v", dir, err)
		}
	}

	// 1. The route is stored at its COMPOSED path. This is the claim the whole example
	//    exists to make: the prefix lives in main, the leaf path lives in another
	//    function, and neither file alone contains "/api/v2/orders/{id}".
	var composed, bare bool
	for _, f := range eng.Store().ByKind(facts.KindRoute) {
		switch {
		case strings.Contains(f.Name, "/api/v2/orders"):
			composed = true
		case strings.HasSuffix(f.Name, "/orders/{id}") && !strings.Contains(f.Name, "/api/v2"):
			bare = true
		}
	}
	if !composed {
		t.Error("the api route is no longer stored at its composed path — the example's central claim has stopped being true")
	}
	if bare {
		t.Error("a route is stored at its bare, un-composed path; a client calling the real path would not resolve to it")
	}

	// 2. The report shows both a resolution and a miss. The miss is not incidental —
	//    the README argues that reporting a gap beats inventing an edge, and an example
	//    where everything resolved would quietly drop that half of the argument.
	report := coverage.Build(eng.Store(), "")
	if len(report) == 0 {
		t.Fatal("no service nodes: the two repos did not link into one graph")
	}
	var resolved, unresolved int
	for _, s := range report {
		resolved += s.Resolved()
		unresolved += s.UnresolvedTotal
	}
	if resolved == 0 {
		t.Error("nothing resolved between the two services; the example no longer demonstrates cross-repo linking")
	}
	if unresolved == 0 {
		t.Error("everything resolved — the deliberately-dynamic call in web/client.go is meant to stay unresolved, and the README explains why")
	}
}
