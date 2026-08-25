package intentcheck

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// declaredDepFact is the intent fact internal/intent compiles a dependencies:
// entry into.
func declaredDepFact(repo, name, ecosystem, purpose string, safety bool) facts.Fact {
	props := map[string]any{
		"intent_kind":  "dependency",
		"package_name": name,
		"purpose":      purpose,
		"safety_path":  safety,
		"source":       "enola-intent.yaml",
	}
	if ecosystem != "" {
		props["ecosystem"] = ecosystem
	}
	return facts.Fact{Kind: facts.KindIntent, Name: "depends on " + name, Repo: repo, File: "enola-intent.yaml", Props: props}
}

// measuredPkgFact is the fact the manifests extractor emits.
func measuredPkgFact(repo, purl, name, ecosystem, manifest string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindDependency, Name: purl, Repo: repo, File: manifest,
		Props: map[string]any{
			"type": facts.TypePackage, "ecosystem": ecosystem,
			"package_name": name, "manifest": manifest, "pinned": true,
		},
	}
}

func titled(insights []facts.Insight, prefix string) (facts.Insight, bool) {
	for _, in := range insights {
		if strings.HasPrefix(in.Title, prefix) {
			return in, true
		}
	}
	return facts.Insight{}, false
}

// A repository declaring nothing about dependencies is UNASKED. Its manifests
// are measured and none of it verdicts — adoption is per-section, exactly as it
// is for seams.
func TestDependencies_UndeclaredRepoIsUnasked(t *testing.T) {
	got := explain(t,
		declaredDepFact("backend", "rails", "", "the web framework", false),
		measuredPkgFact("frontend", "pkg:npm/lodash", "lodash", "npm", "package.json"),
		measuredPkgFact("backend", "pkg:gem/rails", "rails", "rubygems", "Gemfile"),
	)
	if in, ok := titled(got, "Undeclared dependencies: frontend"); ok {
		t.Fatalf("a repo that declared nothing must not verdict: %s", in.Title)
	}
	if in, ok := titled(got, "Undeclared dependencies"); ok {
		t.Fatalf("backend declared its only package: %s", in.Title)
	}
}

// A package the manifests carry and the declaration does not is a set
// difference between two measured lists: exact, so 1.0, and reported once per
// repository rather than once per package.
func TestDependencies_UndeclaredIsExactAndRolledUp(t *testing.T) {
	got := explain(t,
		declaredDepFact("backend", "rails", "", "the web framework", false),
		measuredPkgFact("backend", "pkg:gem/rails", "rails", "rubygems", "Gemfile"),
		measuredPkgFact("backend", "pkg:gem/pg", "pg", "rubygems", "Gemfile"),
		measuredPkgFact("backend", "pkg:gem/sidekiq", "sidekiq", "rubygems", "Gemfile"),
	)
	in, ok := titled(got, "Undeclared dependencies: backend")
	if !ok {
		t.Fatalf("no undeclared finding: %+v", got)
	}
	if in.Confidence != 1.0 {
		t.Fatalf("a set difference is exact, got %v", in.Confidence)
	}
	if !strings.Contains(in.Title, "2 packages") {
		t.Fatalf("title should count both: %q", in.Title)
	}
	var named []string
	for _, e := range in.Evidence {
		named = append(named, e.Fact)
	}
	if strings.Join(named, ",") != "pkg:gem/pg,pkg:gem/sidekiq" {
		t.Fatalf("evidence = %v, want the two undeclared packages sorted", named)
	}
	if count := len(got); count != 1 {
		t.Fatalf("one finding per repo, got %d: %+v", count, got)
	}
}

// A declaration naming a package no manifest carries is 0.8: the dependency was
// removed and the declaration went stale, or the manifest form eluded the
// extractor, and the facts cannot tell those apart.
func TestDependencies_DeclaredButUnmeasuredIsAnEstimate(t *testing.T) {
	got := explain(t,
		declaredDepFact("backend", "rails", "", "the web framework", false),
		declaredDepFact("backend", "sorbet", "", "type checking", false),
		measuredPkgFact("backend", "pkg:gem/rails", "rails", "rubygems", "Gemfile"),
	)
	in, ok := titled(got, "Declared dependency not measured: sorbet")
	if !ok {
		t.Fatalf("no finding: %+v", got)
	}
	if in.Confidence != 0.8 {
		t.Fatalf("an absence must not present as a certainty, got %v", in.Confidence)
	}
}

// An ecosystem narrows the claim. A declaration for the npm package does not
// cover a gem of the same name, and says so from both directions.
func TestDependencies_EcosystemNarrowsTheClaim(t *testing.T) {
	got := explain(t,
		declaredDepFact("app", "parallel", "npm", "worker pool", false),
		measuredPkgFact("app", "pkg:gem/parallel", "parallel", "rubygems", "Gemfile"),
	)
	if _, ok := titled(got, "Undeclared dependencies: app"); !ok {
		t.Fatalf("the gem is not covered by an npm declaration: %+v", got)
	}
	if _, ok := titled(got, "Declared dependency not measured: parallel"); !ok {
		t.Fatalf("the npm package was declared and not measured: %+v", got)
	}

	// Without the narrowing, the same declaration covers it.
	wide := explain(t,
		declaredDepFact("app", "parallel", "", "worker pool", false),
		measuredPkgFact("app", "pkg:gem/parallel", "parallel", "rubygems", "Gemfile"),
	)
	if len(wide) != 0 {
		t.Fatalf("an unnarrowed declaration covers the name in any ecosystem: %+v", wide)
	}
}

// A repository whose manifests were never read is unasked, not dangling: no
// extractor could have proven the declaration either way.
func TestDependencies_NoManifestReadIsNotDangling(t *testing.T) {
	got := explain(t, declaredDepFact("backend", "rails", "", "the web framework", false))
	if in, ok := titled(got, "Declared dependency not measured"); ok {
		t.Fatalf("a repo with no package facts must be unasked: %s", in.Title)
	}
}
