// Package emberresolver binds Ember's convention-named references — .hbs
// template invocations and @service injections — to the symbols that declare
// them.
package emberresolver

import (
	"context"
	"log"
	"path/filepath"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/plugin"
)

// Binder resolves Ember resolver-convention names after extraction, where the
// whole store is visible.
//
// WHY A BINDER. A classic Handlebars template invokes `<Core::Modal />` or
// `{{format-date …}}` with no import to anchor on: the name maps to a file by
// Ember's resolver layout (components/core/modal.*, helpers/format-date.*), and
// the symbol declared IN that file is only known once every file has been
// extracted. The same is true of `@service store` — the target is whatever class
// app/services/store.ts actually declares, which need not be named `Store`. The
// per-file extractor records the names; this binder joins them.
//
// WHY IT SKIPS ON AMBIGUITY. Resolution requires exactly one candidate file and
// exactly one plausible symbol in it. Two matches mean the convention alone
// cannot decide — grpcimpl's precedent is to skip rather than guess, and a wrong
// edge here would feed impact_analysis. Misses are recorded on the template's
// file_ref carrier (ember_unresolved), so "not resolved" never reads as "no
// consumers".
//
// It is post-link: all references are within one repo, like http-handler.
type Binder struct{}

// New returns the binder.
func New() *Binder { return &Binder{} }

func (b *Binder) Name() string { return "ember-resolver" }

func (b *Binder) Stage() plugin.BindStage { return plugin.StagePostLink }

// templateProp/invocationsProp/ownerFileProp/servicesProp mirror the constants
// the TypeScript extractor's Ember pass emits. Spelled here rather than imported
// so this package — like every binder — depends only on facts and plugin.
const (
	templateProp    = "ember_template"
	invocationsProp = "ember_invocations"
	ownerFileProp   = "ember_owner_file"
	servicesProp    = "ember_injected_services"
	unresolvedProp  = "ember_unresolved"
)

var sourceExts = []string{".ts", ".js", ".gts", ".gjs", ".hbs"}

func (b *Binder) Bind(_ context.Context, store *facts.Store) error {
	idx := buildIndex(store)
	bound := 0

	type templateWork struct {
		ownerSymbol string
		targets     []string
		unresolved  []string
	}
	work := map[string]*templateWork{}

	for _, f := range store.ByKind(facts.KindFileRef) {
		if !f.PropBool(templateProp) {
			continue
		}
		w := &templateWork{}
		if owner := f.PropString(ownerFileProp); owner != "" {
			w.ownerSymbol = idx.primarySymbolIn(f.Repo, owner)
		} else {
			w.ownerSymbol = idx.primarySymbolIn(f.Repo, f.Name)
		}
		for _, name := range propStrings(f.Props[invocationsProp]) {
			if target := idx.resolveInvocation(f.Repo, name, f.Name); target != "" {
				w.targets = append(w.targets, target)
			} else {
				w.unresolved = append(w.unresolved, name)
			}
		}
		work[f.Name] = w
	}

	injections := map[string][]string{}
	for _, s := range store.ByKind(facts.KindSymbol) {
		names := propStrings(s.Props[servicesProp])
		if len(names) == 0 {
			continue
		}
		var targets []string
		for _, name := range names {
			if target := idx.resolveService(s.Repo, name); target != "" {
				targets = append(targets, target)
			}
		}
		if len(targets) > 0 {
			sort.Strings(targets)
			injections[s.Name] = targets
		}
	}

	store.UpdateWhere(func(f *facts.Fact) {
		if f.Kind == facts.KindFileRef {
			if w, ok := work[f.Name]; ok && f.PropBool(templateProp) {
				if w.ownerSymbol == "" {
					for _, t := range w.targets {
						if !f.HasRelation(facts.RelCalls, t) {
							f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelCalls, Target: t})
							bound++
						}
					}
				}
				if len(w.unresolved) > 0 {
					sort.Strings(w.unresolved)
					f.Props[unresolvedProp] = w.unresolved
				}
			}
			return
		}
		if f.Kind != facts.KindSymbol {
			return
		}
		if targets, ok := injections[f.Name]; ok {
			for _, t := range targets {
				if t != f.Name && !f.HasRelation(facts.RelInjects, t) {
					f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelInjects, Target: t})
					bound++
				}
			}
		}
		for _, w := range work {
			if w.ownerSymbol != f.Name {
				continue
			}
			for _, t := range w.targets {
				if t != f.Name && !f.HasRelation(facts.RelCalls, t) {
					f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelCalls, Target: t})
					bound++
				}
			}
		}
	})

	if bound > 0 {
		log.Printf("[binder:ember-resolver] bound %d Ember reference(s)", bound)
	}
	return nil
}

// index holds, per repo, the file paths that can back a resolver name and the
// symbols each file declares.
type index struct {
	files       map[string]map[string]bool
	fileSymbols map[string]map[string][]symbolInfo
}

type symbolInfo struct {
	name      string
	exported  bool
	component bool
	class     bool
	receiver  bool
}

func buildIndex(store *facts.Store) *index {
	idx := &index{
		files:       map[string]map[string]bool{},
		fileSymbols: map[string]map[string][]symbolInfo{},
	}
	addFile := func(repo, file string) {
		if file == "" {
			return
		}
		if idx.files[repo] == nil {
			idx.files[repo] = map[string]bool{}
		}
		idx.files[repo][filepath.ToSlash(file)] = true
	}
	for _, s := range store.ByKind(facts.KindSymbol) {
		addFile(s.Repo, s.File)
		if idx.fileSymbols[s.Repo] == nil {
			idx.fileSymbols[s.Repo] = map[string][]symbolInfo{}
		}
		file := filepath.ToSlash(s.File)
		idx.fileSymbols[s.Repo][file] = append(idx.fileSymbols[s.Repo][file], symbolInfo{
			name:      s.Name,
			exported:  s.PropBool("exported"),
			component: s.PropString("web_component") == "component",
			class:     s.PropString("symbol_kind") == facts.SymbolClass,
			receiver:  s.PropString("receiver") != "",
		})
	}
	for _, f := range store.ByKind(facts.KindFileRef) {
		if f.PropBool(templateProp) {
			addFile(f.Repo, f.File)
		}
	}
	return idx
}

// resolveInvocation maps one template invocation name to a symbol: the name
// dasherizes to a resolver path fragment, the fragment must match exactly one
// file (the invoking template itself excluded), and that file must yield exactly
// one primary symbol.
func (idx *index) resolveInvocation(repo, name, selfFile string) string {
	var fragments []string
	if name[0] >= 'A' && name[0] <= 'Z' {
		segs := strings.Split(name, "::")
		for i, s := range segs {
			segs[i] = dasherize(s)
		}
		fragments = []string{"components/" + strings.Join(segs, "/")}
	} else {
		fragments = []string{"components/" + name, "helpers/" + name}
	}
	for _, frag := range fragments {
		if file := idx.uniqueFile(repo, frag, selfFile); file != "" {
			if sym := idx.primarySymbolIn(repo, file); sym != "" {
				return sym
			}
		}
	}
	return ""
}

func (idx *index) resolveService(repo, name string) string {
	if file := idx.uniqueFile(repo, "services/"+name, ""); file != "" {
		return idx.primarySymbolIn(repo, file)
	}
	return ""
}

// uniqueFile returns the single file backing "app/<fragment><ext>" (or the
// folder-index form) across the repo's files, or "" when zero or several match.
// Anchoring on the `app/` tree is Ember's own resolver rule — a lookalike path
// elsewhere (a style-guide template under app/templates/…/components/, an
// in-repo addon) is not resolvable by name and must not shadow the real one.
// Several matches can still occur in a monorepo holding more than one Ember
// app; that is a genuine ambiguity and skips.
// Extensions are tried in priority order and the first that matches wins
// outright: a classic component is a co-located pair (field.ts + field.hbs) that
// backs ONE resolver name, so the class file must not be read as ambiguous with
// its own template. Two matches at the SAME extension — two Ember apps in one
// monorepo — are a genuine ambiguity and skip.
func (idx *index) uniqueFile(repo, fragment, selfFile string) string {
	for _, ext := range sourceExts {
		for _, anchored := range []string{"app/" + fragment + ext, "app/" + fragment + "/index" + ext} {
			found := ""
			ambiguous := false
			for file := range idx.files[repo] {
				if file == selfFile {
					continue
				}
				if file != anchored && !strings.HasSuffix(file, "/"+anchored) {
					continue
				}
				if found != "" && found != file {
					ambiguous = true
				}
				found = file
			}
			if ambiguous {
				return ""
			}
			if found != "" {
				return found
			}
		}
	}
	return ""
}

// primarySymbolIn picks the one symbol a resolver name means in a file: the
// single exported component, else the single exported top-level class, else the
// single exported top-level symbol. Anything plural is ambiguous and skipped.
func (idx *index) primarySymbolIn(repo, file string) string {
	syms := idx.fileSymbols[repo][filepath.ToSlash(file)]
	pick := func(match func(symbolInfo) bool) (string, int) {
		name, n := "", 0
		for _, s := range syms {
			if s.receiver || !match(s) {
				continue
			}
			if name != s.name {
				n++
				name = s.name
			}
		}
		return name, n
	}
	if name, n := pick(func(s symbolInfo) bool { return s.exported && s.component }); n == 1 {
		return name
	}
	if name, n := pick(func(s symbolInfo) bool { return s.exported && s.class }); n == 1 {
		return name
	}
	if name, n := pick(func(s symbolInfo) bool { return s.exported }); n == 1 {
		return name
	}
	return ""
}

func propStrings(v any) []string {
	switch vv := v.(type) {
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, x := range vv {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func dasherize(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r - 'A' + 'a')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
