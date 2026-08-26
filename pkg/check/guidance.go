package check

import (
	"github.com/enola-labs/enola/internal/diff"
	"github.com/enola-labs/enola/internal/explainers/constraints"
	"github.com/enola-labs/enola/internal/facts"
)

func AttachGuidance(v Verdict, store *facts.Store) Verdict {
	if store == nil || v.Diff == nil {
		return v
	}
	switch v.Status {
	case StatusClean, StatusRegression, StatusPartialClean, StatusPartialRegression:
	default:
		return v
	}
	v.Guidance = constraints.GuidanceForFiles(store, changedFiles(v.Diff))
	return v
}

func changedFiles(d *diff.SnapshotDiff) []constraints.ChangedFile {
	seen := map[string]bool{}
	var out []constraints.ChangedFile
	add := func(f facts.Fact) {
		// A repository-scoped fact carries a LABEL in File, not a path: an
		// extraction fact's File is the repository's directory name. Nobody edited
		// that "file", and every consumer here reads File as something a change
		// touched — so letting it through resolves to the root module and gives it
		// a routing line of its own on any change that moves an extractor's
		// coverage counters, which is most changes in a Ruby or TypeScript tree.
		if f.Kind == facts.KindExtraction {
			return
		}
		if f.File == "" || seen[f.File] {
			return
		}
		seen[f.File] = true
		out = append(out, constraints.ChangedFile{Path: f.File, Repo: f.Repo})
	}
	for _, f := range d.FactsAdded {
		add(f)
	}
	for _, f := range d.FactsRemoved {
		add(f)
	}
	for _, c := range d.FactsChanged {
		add(c.Before)
		add(c.After)
	}
	return out
}
