package intentcheck

import (
	"fmt"
	"sort"

	"github.com/enola-labs/enola/internal/facts"
)

// declaredPackage is one dependencies: entry, compiled.
type declaredPackage struct {
	name string
	// ecosystem narrows the entry to one packaging system. Empty covers the
	// name wherever it is measured.
	ecosystem string
	purpose   string
	safety    bool
	source    string
}

// measuredPackage is one package a manifest declared.
type measuredPackage struct {
	fact      string
	name      string
	ecosystem string
	manifest  string
}

// dependencyVerdicts diffs a repository's DECLARED external dependencies
// against the ones its manifests actually declare.
//
// It is the seam verdict one level down, with the same two-sided honesty. A
// package the manifests carry and the declaration does not is a set difference
// between two measured lists — exact, so `1.0`. A package the declaration names
// and no manifest carries is `0.8`, because the two readings that reach it are
// indistinguishable from here: the dependency was removed and the declaration
// went stale, or an ecosystem's manifest form eluded the extractor.
//
// A repository that declares no dependencies at all is UNASKED. Adoption is
// per-repo and per-section, exactly as it is for seams: a repository listing
// three packages it cares about has not thereby claimed the other four hundred
// are undeclared, so the undeclared verdict only fires for a repository whose
// declaration says something about dependencies in the first place.
func dependencyVerdicts(store *facts.Store) []facts.Insight {
	all := store.FactsRef()

	// declaredBy is keyed by owner repo. The value is every declared entry,
	// which may narrow on ecosystem or cover a name in all of them.
	declaredBy := map[string][]declaredPackage{}
	for _, f := range all {
		if f.Kind != facts.KindIntent || f.PropString("intent_kind") != "dependency" {
			continue
		}
		owner := f.PropString("intent_owner")
		if owner == "" {
			owner = f.Repo
		}
		declaredBy[owner] = append(declaredBy[owner], declaredPackage{
			name:      f.PropString("package_name"),
			ecosystem: f.PropString("ecosystem"),
			purpose:   f.PropString("purpose"),
			safety:    f.PropBool("safety_path"),
			source:    f.PropString("source"),
		})
	}
	if len(declaredBy) == 0 {
		return nil
	}

	// measuredBy is every package the manifests declared, per repo.
	measuredBy := map[string][]measuredPackage{}
	// measuredRepos records which repositories the manifests extractor spoke
	// about at all. A repo with no package facts is one whose manifests were
	// never read — a language enola does not measure a manifest for, or a
	// snapshot taken before this extractor existed — and a declaration there is
	// unasked rather than dangling.
	measuredRepos := map[string]bool{}
	for _, f := range all {
		if f.Kind != facts.KindDependency || f.PropString("type") != facts.TypePackage {
			continue
		}
		measuredRepos[f.Repo] = true
		measuredBy[f.Repo] = append(measuredBy[f.Repo], measuredPackage{
			fact:      f.Name,
			name:      f.PropString("package_name"),
			ecosystem: f.PropString("ecosystem"),
			manifest:  f.PropString("manifest"),
		})
	}

	owners := make([]string, 0, len(declaredBy))
	for owner := range declaredBy {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	var out []facts.Insight
	for _, owner := range owners {
		if !measuredRepos[owner] {
			continue // no manifest was read for this repo: not asked, never failed
		}
		declared := declaredBy[owner]
		measured := measuredBy[owner]

		// covers reports whether a declared entry claims a measured package. An
		// entry with no ecosystem covers the name wherever it is measured.
		covers := func(d declaredPackage, m measuredPackage) bool {
			if d.name != m.name {
				return false
			}
			return d.ecosystem == "" || d.ecosystem == m.ecosystem
		}

		var undeclared []measuredPackage
		for _, m := range measured {
			claimed := false
			for _, d := range declared {
				if covers(d, m) {
					claimed = true
					break
				}
			}
			if !claimed {
				undeclared = append(undeclared, m)
			}
		}
		sort.Slice(undeclared, func(i, j int) bool { return undeclared[i].fact < undeclared[j].fact })

		// One finding per repository rather than one per package. A repository
		// adopting this section has hundreds of undeclared packages on the first
		// run, and four hundred findings is a report nobody reads — the same
		// reason hotspots is capped.
		if len(undeclared) > 0 {
			out = append(out, facts.Insight{
				Title: fmt.Sprintf("Undeclared dependencies: %s pulls in %s nothing declares", owner, plural(len(undeclared), "package", "packages")),
				Description: fmt.Sprintf(
					"%s declares %s and its manifests carry %s. Nothing declares the %s named below — set difference between what the declaration states and what the manifests say, which either holds or it does not. Declare each with the purpose it serves, or remove it.",
					owner, plural(len(declared), "dependency", "dependencies"), plural(len(measured), "package", "packages"),
					plural(len(undeclared), "package", "packages")),
				Confidence: 1.0,
				Evidence:   dependencyEvidence(undeclared),
				Actions: []string{
					"Add each package to dependencies: with the purpose it serves",
					"Remove the ones nothing needs",
				},
			})
		}

		for _, d := range declared {
			found := false
			for _, m := range measured {
				if covers(d, m) {
					found = true
					break
				}
			}
			if found {
				continue
			}
			where := d.ecosystem
			if where == "" {
				where = "any ecosystem"
			}
			out = append(out, facts.Insight{
				Title: fmt.Sprintf("Declared dependency not measured: %s (%s)", d.name, owner),
				Description: fmt.Sprintf(
					"The declaration names %q in %s and no manifest enola read declares it. Two readings reach that and the facts cannot tell them apart: the dependency was removed and the declaration went stale, or its manifest form is one the extractor does not read. Hence 0.8 — an absence is never presented as a certainty.",
					d.name, where),
				Confidence: 0.8,
				Evidence:   []facts.Evidence{{Fact: "depends on " + d.name, Detail: "declared in " + d.source}},
				Actions: []string{
					"Remove the declaration if the dependency is gone",
					"Check whether the manifest form is one enola reads (docs/INTENT.md)",
				},
			})
		}
	}
	return out
}

// dependencyEvidence names the undeclared packages, capped, with the residual
// counted rather than dropped: a list that silently stops is one a reader
// mistakes for the whole answer.
func dependencyEvidence(deps []measuredPackage) []facts.Evidence {
	const maxSamples = 25
	out := make([]facts.Evidence, 0, maxSamples+1)
	for i, d := range deps {
		if i == maxSamples {
			out = append(out, facts.Evidence{
				Detail: fmt.Sprintf("and %d more not listed", len(deps)-maxSamples),
			})
			break
		}
		out = append(out, facts.Evidence{Fact: d.fact, Detail: "declared in " + d.manifest})
	}
	return out
}

// plural renders a count with an explicit plural form.
func plural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}
