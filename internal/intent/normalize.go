package intent

import (
	"github.com/enola-labs/enola/internal/factpath"
)

// Path fields in a declaration are normalised to forward slashes the moment the
// YAML is unmarshalled, before validation and before compilation.
//
// A declaration is portable text: the same enola-intent.yaml is read on a Linux
// runner and a Windows laptop, and it must select the same code in both. Yet the
// author's shell, editor and file explorer all hand them backslashes on Windows,
// so `src\lib\**` is what gets pasted in. Without this, that declaration is
// rejected outright by the match dialect (a backslash is not a legal glob
// character) or — worse for `layers:`, which had no form validation at all —
// accepted, compiled, and matched against nothing, leaving a layer order that
// lints clean and governs zero modules (issue #242).
//
// Normalising is the right answer rather than rejecting: in a path field a
// backslash can only mean a directory separator, so there is nothing to warn
// about and nothing the author would do differently if told.
//
// Only PATH fields are touched. Names, name patterns and where-predicates are
// left exactly as written — a name_pattern may legitimately have to match a PHP
// class whose namespace is backslash-separated.

// Normalize rewrites every path field of a declaration into fact-path form.
func (d *Declaration) Normalize() {
	if d == nil {
		return
	}
	for i := range d.Layers {
		normalizePaths(d.Layers[i].Paths)
	}
	normalizeComponents(d.Components)
	normalizeRules(d.Rules)
}

// Normalize rewrites every path field of one enola/constraints file.
func (f *ConstraintsFile) Normalize() {
	if f == nil {
		return
	}
	normalizeComponents(f.Components)
	normalizeRules(f.Rules)
	for i := range f.UseRecipe {
		for role := range f.UseRecipe[i].Bind {
			b := f.UseRecipe[i].Bind[role]
			normalizePaths(b.Match)
			f.UseRecipe[i].Bind[role] = b
		}
	}
}

// Normalize rewrites every path field of a recipe's rule templates.
func (r *Recipe) Normalize() {
	if r == nil {
		return
	}
	normalizeRules(r.Rules)
}

// Normalize rewrites every path field a knowledge page declares: its anchors,
// and the layer orders it states on behalf of the repos it names.
func (p *PageIntent) Normalize() {
	if p == nil {
		return
	}
	if p.Page != nil {
		for i := range p.Page.Anchors {
			p.Page.Anchors[i].Path = factpath.Declared(p.Page.Anchors[i].Path)
		}
	}
	for i := range p.Layers {
		for j := range p.Layers[i].Order {
			normalizePaths(p.Layers[i].Order[j].Paths)
		}
	}
}

func normalizeComponents(cc []ConstraintComponent) {
	for i := range cc {
		normalizePaths(cc[i].Match)
	}
}

func normalizeRules(rr []ConstraintRule) {
	for i := range rr {
		normalizePaths(rr[i].Exemplars)
	}
}

// normalizePaths rewrites a slice of path patterns in place. Empty entries are
// left alone: an empty pattern is a validation problem the caller reports in its
// own words, and normalising it would not change it anyway.
func normalizePaths(paths []string) {
	for i, p := range paths {
		if p == "" {
			continue
		}
		paths[i] = factpath.Declared(p)
	}
}
