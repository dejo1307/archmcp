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
