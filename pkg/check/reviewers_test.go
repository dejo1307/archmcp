package check

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/facts"
)

// storeWithModules builds a store holding just the named production modules.
func storeWithModules(names ...string) *facts.Store {
	store := facts.NewStore()
	for _, n := range names {
		store.Add(facts.Fact{Kind: facts.KindModule, Name: n, File: n + "/f.go"})
	}
	return store
}

// importGraphStore adds a dependency fact so that `from` imports `to`, using the shape
// BuildModuleGraph reads.
func importGraphStore(from, to string) *facts.Store {
	store := storeWithModules(from, to)
	store.Add(facts.Fact{
		Kind: facts.KindDependency, Name: from + " -> " + to, File: from + "/f.go",
		Relations: []facts.Relation{{Kind: facts.RelImports, Target: to}},
	})
	return store
}

// authorshipOf builds an Authorship directly, so the join is tested without a repo.
func authorshipOf(mods map[string]map[string]int) *Authorship {
	a := &Authorship{Window: 500, Commits: 1,
		byModule: map[string]ModuleAuthorship{}, authors: map[string]bool{}}
	for mod, authors := range mods {
		total := 0
		for name, n := range authors {
			total += n
			a.authors[name] = true
		}
		a.byModule[mod] = summarise(mod, authors, total)
	}
	return a
}

func touchedDelta(file string) *diff.SnapshotDiff {
	return &diff.SnapshotDiff{
		Comparability: diff.Comparability{Comparable: true},
		FactsAdded:    []facts.Fact{{Kind: facts.KindSymbol, Name: "X", File: file}},
	}
}

func cleanVerdict(d *diff.SnapshotDiff) Verdict {
	return Verdict{Status: StatusClean, Diff: d}
}

// The join: ada edits pkg/auth as a stranger while owning pkg/tokens, which imports
// pkg/auth. That is the Major-Minor-Dependency the paper found in 52% of Vista
// binaries, and the one thing this whole feature exists to print.
func TestMajorMinorDependencyFiresOnDependentTheActorOwns(t *testing.T) {
	store := importGraphStore("pkg/tokens", "pkg/auth")
	a := authorshipOf(map[string]map[string]int{
		"pkg/auth":   {"bob": 99, "ada": 1},
		"pkg/tokens": {"ada": 90, "bob": 10},
	})

	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/auth/a.go")), store, a, "ada")

	if v.Reviewers == nil || len(v.Reviewers.Routes) != 1 {
		t.Fatalf("want one route, got %+v", v.Reviewers)
	}
	route := v.Reviewers.Routes[0]
	if route.Module != "pkg/auth" {
		t.Fatalf("Module = %q, want pkg/auth", route.Module)
	}
	if !route.ActorIsMinor {
		t.Error("ada holds 1% of pkg/auth and must read as a minor contributor")
	}
	if route.Owner != "bob" {
		t.Errorf("Owner = %q, want bob", route.Owner)
	}
	if len(route.ViaDependents) != 1 || route.ViaDependents[0].Dependent != "pkg/tokens" {
		t.Fatalf("want the major-minor-dependency on pkg/tokens, got %+v", route.ViaDependents)
	}
}

// The direction is the entire claim. An owner of pkg/auth reaching into pkg/tokens
// which pkg/auth imports is the harmless case, and reporting it would invert the
// paper's finding into advice to avoid touching your own dependencies.
func TestMajorMinorDependencyDoesNotFireOnTheReverseDirection(t *testing.T) {
	// pkg/auth imports pkg/tokens — the opposite of the case above.
	store := importGraphStore("pkg/auth", "pkg/tokens")
	a := authorshipOf(map[string]map[string]int{
		"pkg/auth":   {"bob": 99, "ada": 1},
		"pkg/tokens": {"ada": 90, "bob": 10},
	})

	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/auth/a.go")), store, a, "ada")

	route := v.Reviewers.Routes[0]
	if !route.ActorIsMinor {
		t.Fatal("ada is still a minor contributor to pkg/auth")
	}
	if len(route.ViaDependents) != 0 {
		t.Errorf("nothing that ada owns imports pkg/auth, got %+v", route.ViaDependents)
	}
}

func TestNoDependencyReportedWhenActorIsMajorInTheTouchedModule(t *testing.T) {
	store := importGraphStore("pkg/tokens", "pkg/auth")
	a := authorshipOf(map[string]map[string]int{
		"pkg/auth":   {"ada": 80, "bob": 20},
		"pkg/tokens": {"ada": 90, "bob": 10},
	})

	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/auth/a.go")), store, a, "ada")

	route := v.Reviewers.Routes[0]
	if route.ActorIsMinor {
		t.Error("ada owns 80% of pkg/auth and is not a stranger to it")
	}
	if len(route.ViaDependents) != 0 {
		t.Error("the join only applies where the actor is minor in the touched module")
	}
}

func TestModuleWithNoClearOwnerReportsNone(t *testing.T) {
	store := storeWithModules("pkg/auth")
	a := authorshipOf(map[string]map[string]int{
		"pkg/auth": {"a": 20, "b": 20, "c": 20, "d": 20, "e": 20},
	})

	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/auth/a.go")), store, a, "")

	route := v.Reviewers.Routes[0]
	if route.Owner != "" {
		t.Errorf("Owner = %q, want none above the 50%% line", route.Owner)
	}
	if route.Total != 5 || route.Minor != 0 {
		t.Errorf("Total/Minor = %d/%d, want 5/0 — 20%% each clears the 5%% line", route.Total, route.Minor)
	}
}

// A module the window never touched is absent rather than zeroed: "nobody has changed
// this in 500 commits" must not print as "this module has no owner".
func TestModuleAbsentFromTheWindowIsNotRouted(t *testing.T) {
	store := storeWithModules("pkg/auth")
	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/auth/a.go")), store,
		authorshipOf(map[string]map[string]int{}), "ada")

	if len(v.Reviewers.Routes) != 0 {
		t.Errorf("want no routes, got %+v", v.Reviewers.Routes)
	}
}

// The invariant this whole feature is fenced by: routing is steering, and a
// correlational signal must never reach the gate.
func TestReviewersNeverGrade(t *testing.T) {
	store := importGraphStore("pkg/tokens", "pkg/auth")
	a := authorshipOf(map[string]map[string]int{
		"pkg/auth":   {"bob": 99, "ada": 1},
		"pkg/tokens": {"ada": 90, "bob": 10},
	})

	for _, status := range []Status{StatusClean, StatusRegression, StatusPartialClean, StatusPartialRegression} {
		before := Verdict{
			Status:   status,
			Diff:     touchedDelta("pkg/auth/a.go"),
			Failures: []facts.Insight{{Title: "cycle", Confidence: 1}},
			Breaches: []Breach{{}},
		}
		after := AttachReviewers(before, store, a, "ada")

		if after.Reviewers == nil || len(after.Reviewers.Routes) == 0 {
			t.Fatalf("%s: expected routing to be attached", status)
		}
		if after.Status != before.Status {
			t.Errorf("%s: Status changed to %s", status, after.Status)
		}
		if after.ExitCode() != before.ExitCode() {
			t.Errorf("%s: ExitCode changed %d -> %d", status, before.ExitCode(), after.ExitCode())
		}
		if len(after.Failures) != len(before.Failures) {
			t.Errorf("%s: Failures changed %d -> %d", status, len(before.Failures), len(after.Failures))
		}
		if len(after.Breaches) != len(before.Breaches) {
			t.Errorf("%s: Breaches changed %d -> %d", status, len(before.Breaches), len(after.Breaches))
		}
	}
}

// A verdict that declined to grade has no delta worth routing.
func TestReviewersSkipNonGradedStatuses(t *testing.T) {
	store := storeWithModules("pkg/auth")
	a := authorshipOf(map[string]map[string]int{"pkg/auth": {"ada": 10}})
	for _, status := range []Status{StatusIncomparable, StatusUsageError} {
		v := AttachReviewers(Verdict{Status: status, Diff: touchedDelta("pkg/auth/a.go")}, store, a, "ada")
		if v.Reviewers != nil {
			t.Errorf("%s: expected no routing, got %+v", status, v.Reviewers)
		}
	}
}

func TestRenderReviewersNamesTheDependencyAndTheReviewer(t *testing.T) {
	store := importGraphStore("pkg/tokens", "pkg/auth")
	a := authorshipOf(map[string]map[string]int{
		"pkg/auth":   {"bob": 99, "ada": 1},
		"pkg/tokens": {"ada": 90, "bob": 10},
	})
	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/auth/a.go")), store, a, "ada")

	var sb strings.Builder
	v.writeReviewers(&sb)
	out := sb.String()

	for _, want := range []string{
		"Reviewers for this change (1)",
		"never graded",
		"owner: bob (99%)",
		"you are a minor contributor here (1%)",
		"you own pkg/tokens (90%), which imports pkg/auth  [major-minor-dependency]",
		"suggested reviewer: bob",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderReviewersIsSilentWhenAbsent(t *testing.T) {
	var sb strings.Builder
	Verdict{Status: StatusClean}.writeReviewers(&sb)
	if sb.String() != "" {
		t.Errorf("a verdict without --reviewers must print nothing, got %q", sb.String())
	}
}

func TestRenderReviewersNamesTheCauseWhenItCouldNotMeasure(t *testing.T) {
	v := Verdict{Status: StatusClean, Reviewers: &Reviewers{Window: 500, Cause: AuthorshipNoGit}}
	var sb strings.Builder
	v.writeReviewers(&sb)
	if !strings.Contains(sb.String(), "no readable git repository") {
		t.Errorf("a declined read must say why, got %q", sb.String())
	}
}

// An actor the window never saw holds 0% of everything. Reporting that as "you are a
// minor contributor here" on every module is how a CI robot's unset git identity turns
// the routing section into noise.
func TestUnknownActorIsSaidOnceAndDropsTheActorFraming(t *testing.T) {
	store := importGraphStore("pkg/tokens", "pkg/auth")
	a := authorshipOf(map[string]map[string]int{
		"pkg/auth":   {"bob": 100},
		"pkg/tokens": {"bob": 100},
	})

	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/auth/a.go")), store, a, "github-actions[bot]")

	if !v.Reviewers.ActorUnknown {
		t.Fatal("an actor absent from the window must be marked unknown")
	}
	route := v.Reviewers.Routes[0]
	if route.ActorIsMinor {
		t.Error("an unknown actor must not be described as a minor contributor")
	}
	if len(route.ViaDependents) != 0 {
		t.Error("the join cannot fire for someone with no commits anywhere")
	}
	if route.Owner != "bob" {
		t.Errorf("owners are still reported; Owner = %q", route.Owner)
	}

	var sb strings.Builder
	v.writeReviewers(&sb)
	if !strings.Contains(sb.String(), "has no commits in the window") {
		t.Errorf("the reason must be said once, got:\n%s", sb.String())
	}
	if strings.Contains(sb.String(), "you are a minor contributor") {
		t.Errorf("actor framing must be dropped, got:\n%s", sb.String())
	}
}

// Knows separates "stranger to this module" from "not a contributor at all".
func TestAuthorshipKnowsOnlyAuthorsInTheWindow(t *testing.T) {
	a := authorshipOf(map[string]map[string]int{"pkg/a": {"ada": 1}})
	if !a.Knows("ada") {
		t.Error("ada committed in the window")
	}
	if a.Knows("zoe") {
		t.Error("zoe did not")
	}
	var nilA *Authorship
	if nilA.Knows("ada") {
		t.Error("a nil Authorship knows nobody")
	}
}

// A module with one commit in the window has a contributor holding 100% of it. That is
// arithmetic, not ownership, and it was the top-ranked major-minor-dependency reported
// on a real repository until minModuleCommits existed.
func TestSparseModuleOwnsNothingAndCannotCarryTheJoin(t *testing.T) {
	store := importGraphStore("pkg/drive-by", "pkg/auth")
	a := authorshipOf(map[string]map[string]int{
		"pkg/auth":     {"bob": 99, "ada": 1},
		"pkg/drive-by": {"ada": 1}, // one commit, ada at 100%
	})

	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/auth/a.go")), store, a, "ada")

	route := v.Reviewers.Routes[0]
	if !route.ActorIsMinor {
		t.Fatal("ada is still a stranger to pkg/auth")
	}
	if len(route.ViaDependents) != 0 {
		t.Errorf("one commit is not ownership; got %+v", route.ViaDependents)
	}
}

func TestSparseModuleNamesNoOwnerButStillReportsWhatWasMeasured(t *testing.T) {
	store := storeWithModules("pkg/thin")
	a := authorshipOf(map[string]map[string]int{"pkg/thin": {"ada": 2}})

	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/thin/a.go")), store, a, "")

	route := v.Reviewers.Routes[0]
	if route.Owner != "" {
		t.Errorf("Owner = %q, want none — two commits name no owner", route.Owner)
	}
	if route.Commits != 2 || route.Total != 1 {
		t.Errorf("the measurement is still reported: Commits/Total = %d/%d, want 2/1", route.Commits, route.Total)
	}
}

// The floor applies to the module's evidence, not to whether somebody is a stranger to
// it: holding none of a thin module still makes you a minor contributor there.
func TestSparseModuleStillMakesAnAbsentActorMinor(t *testing.T) {
	store := storeWithModules("pkg/thin")
	a := authorshipOf(map[string]map[string]int{"pkg/thin": {"bob": 2}})

	v := AttachReviewers(cleanVerdict(touchedDelta("pkg/thin/a.go")), store, a, "bob")

	if v.Reviewers.Routes[0].ActorIsMinor {
		t.Error("bob holds all of pkg/thin; sparse must not invert who is a stranger")
	}
}

// A repository-scoped fact is not a file anybody touched. An extraction fact carries
// the repository's directory name in File, and its coverage counters move on most
// changes to a Ruby or TypeScript tree — so before changedFiles skipped it, every such
// change opened the routing section with a line about the root module, measured over
// the whole repository and telling the reader nothing.
func TestRoutingIgnoresRepositoryScopedFacts(t *testing.T) {
	store := storeWithModules(".", "pkg/auth")
	a := authorshipOf(map[string]map[string]int{
		".":        {"bob": 60, "ada": 10},
		"pkg/auth": {"bob": 99, "ada": 1},
	})
	d := touchedDelta("pkg/auth/a.go")
	// The shape enola actually produces: extcoverage puts filepath.Base(repoPath)
	// in File, so the "path" here is the checkout's directory name.
	d.FactsChanged = []diff.FactChange{{
		Before: facts.Fact{Kind: facts.KindExtraction, Name: "ruby:calls", File: "chatwoot"},
		After:  facts.Fact{Kind: facts.KindExtraction, Name: "ruby:calls", File: "chatwoot"},
	}}

	v := AttachReviewers(cleanVerdict(d), store, a, "ada")

	if v.Reviewers == nil {
		t.Fatal("no routing report")
	}
	for _, route := range v.Reviewers.Routes {
		if route.Module == "." {
			t.Fatalf("root module routed from an extraction fact: %+v", v.Reviewers.Routes)
		}
	}
	if len(v.Reviewers.Routes) != 1 || v.Reviewers.Routes[0].Module != "pkg/auth" {
		t.Fatalf("want the one touched module, got %+v", v.Reviewers.Routes)
	}
}
