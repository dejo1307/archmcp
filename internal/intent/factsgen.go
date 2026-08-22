package intent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// CompileFacts turns one repo's resolved declaration into intent facts — the
// compilation step that makes declared architecture part of the graph it
// governs. Declarations become store facts so snapshots carry them, diffs
// track them, receipts fingerprint them, and the intent explainer reads them
// with no side channel. Fact names are deterministic and human-readable; the
// props carry the joinable structure.
func CompileFacts(d *Declaration) []facts.Fact {
	if d == nil {
		return nil
	}
	base := func(kind string, extra map[string]any) map[string]any {
		props := map[string]any{
			"intent_kind": kind,
			"source":      d.Source,
		}
		if d.Overridden {
			props["overridden"] = true
		}
		for k, v := range extra {
			props[k] = v
		}
		return props
	}
	var out []facts.Fact
	if d.Service.Name != "" {
		out = append(out, facts.Fact{
			Kind:  facts.KindIntent,
			Name:  "service " + d.Service.Name,
			File:  RepoFileName,
			Props: base("service", map[string]any{"service_name": d.Service.Name, "description": d.Service.Description}),
		})
	}
	for _, c := range d.Consumes {
		out = append(out, facts.Fact{
			Kind:  facts.KindIntent,
			Name:  fmt.Sprintf("consumes %s via %s", c.Repo, c.Via),
			File:  RepoFileName,
			Props: base("consumes", map[string]any{"target": c.Repo, "via": c.Via}),
		})
	}
	for _, sv := range d.Serves {
		out = append(out, facts.Fact{
			Kind:  facts.KindIntent,
			Name:  "serves " + sv.Via,
			File:  RepoFileName,
			Props: base("serves", map[string]any{"via": sv.Via, "description": sv.Description}),
		})
	}
	for i, l := range d.Layers {
		out = append(out, facts.Fact{
			Kind:  facts.KindIntent,
			Name:  "layer " + l.Name,
			File:  RepoFileName,
			Props: base("layer", map[string]any{"layer_name": l.Name, "order": i, "paths": append([]string(nil), l.Paths...)}),
		})
	}
	for _, c := range d.Components {
		// Patterns are sorted before joining so the compiled fact — and every
		// fingerprint downstream of it — is a function of the declared SET, not
		// of the YAML order the author happened to write.
		match := append([]string(nil), c.Match...)
		sort.Strings(match)
		extra := map[string]any{
			"component": c.Name,
			"match":     strings.Join(match, " "),
		}
		if c.Service != "" {
			extra["service"] = c.Service
		}
		// Both spellings of the kind narrowing compile to the one kind prop —
		// the predicate's reserved key is the same narrowing, not a second one,
		// so nothing downstream of compilation has to know which spelling the
		// author used.
		if kind := c.FactKind(); kind != "" {
			extra["kind"] = kind
		}
		if c.NamePattern != "" {
			extra["name_pattern"] = c.NamePattern
		}
		if where := EncodeWhere(c.Predicate()); where != "" {
			extra["where"] = where
		}
		// Only a declared ownership fingerprints. An undeclared one is not the
		// same statement as an explicit nothing — the edge-role screen refuses
		// the first and admits the second — so compiling a default here would
		// erase the distinction the declaration turns on.
		if c.Owns != "" {
			extra["owns"] = c.Owns
		}
		if c.Ancestor != "" {
			extra["ancestor"] = c.Ancestor
		}
		if len(c.Public) > 0 {
			public := append([]string(nil), c.Public...)
			sort.Strings(public)
			extra["public"] = strings.Join(public, " ")
		}
		graphComponentProps(c, extra)
		if c.Recipe != "" {
			extra["recipe"] = c.Recipe
			extra["instance"] = c.Instance
			extra["role"] = c.Role
		}
		out = append(out, facts.Fact{
			Kind:  facts.KindIntent,
			Name:  "component: " + c.Name,
			File:  constraintDeclaringFile(c.SourceFile, extra),
			Props: base("component", extra),
		})
	}
	for _, r := range d.Rules {
		// Mode is normalized at compile time: an absent mode and an explicit
		// ratchet declare the same enforcement, so they must fingerprint the
		// same. Guidance defaults to notify — steering's quiet channel — for
		// the same fingerprint reason.
		mode := r.Mode
		if mode == "" {
			if r.Guide != "" {
				mode = "notify"
			} else {
				mode = "ratchet"
			}
		}
		extra := map[string]any{
			"rule":    r.ID,
			"mode":    mode,
			"because": r.Because,
		}
		edgeFormProps(r, extra)
		memberFormProps(r, extra)
		namingAndGuidanceProps(r, extra)
		graphFormProps(r, extra)
		if owns := EncodeOwnership(r.Owns); owns != "" {
			extra["owns"] = owns
		}
		if len(r.Exempt) > 0 {
			extra["exempt"] = EncodeExemptions(r.Exempt)
		}
		if r.Recipe != "" {
			extra["recipe"] = r.Recipe
			extra["instance"] = r.Instance
		}
		out = append(out, facts.Fact{
			Kind:  facts.KindIntent,
			Name:  "rule: " + r.ID,
			File:  constraintDeclaringFile(r.SourceFile, extra),
			Props: base("rule", extra),
		})
	}
	return out
}

// constraintDeclaringFile resolves which file a compiled component or rule
// fact cites: the constraints-directory file that declared it, or the repo
// declaration file for inline entries. A directory-declared entry also
// overrides the fact's source prop (extra wins over base), so govern and
// verdicts name billing.yaml rather than the merged declaration.
func constraintDeclaringFile(sourceFile string, extra map[string]any) string {
	if sourceFile == "" {
		return RepoFileName
	}
	extra["source"] = sourceFile
	return sourceFile
}

// CompilePageFacts turns a page's enola_intent block into intent facts whose
// File is the PAGE — provenance pointing at the decision, not a config
// artifact. Each fact carries intent_owner: the repo the intent is about,
// which the verdicting explainers key on (a page about the mobile app lives in the
// wiki repo; the fact's Repo label says where it was found, intent_owner says
// whose intent it states).
func CompilePageFacts(p *PageIntent, pageFile string) []facts.Fact {
	var out []facts.Fact
	if pg := p.Page; pg != nil {
		props := map[string]any{
			"intent_kind": "page",
			"page_type":   pg.Type,
			"source":      pageFile,
		}
		if pg.Status != "" {
			props["status"] = pg.Status
		}
		if len(pg.Scope) > 0 {
			props["scope"] = append([]string(nil), pg.Scope...)
		}
		if len(pg.Affects) > 0 {
			props["affects"] = append([]string(nil), pg.Affects...)
		}
		if len(pg.Origin) > 0 {
			props["origin"] = append([]string(nil), pg.Origin...)
		}
		out = append(out, facts.Fact{
			Kind:  facts.KindIntent,
			Name:  "page: " + pageFile,
			File:  pageFile,
			Props: props,
		})
		for _, r := range pg.Relations {
			out = append(out, facts.Fact{
				Kind: facts.KindIntent,
				Name: fmt.Sprintf("%s %s %s", pageFile, r.Rel, r.To),
				File: pageFile,
				Props: map[string]any{
					"intent_kind": "relation",
					"rel":         r.Rel,
					"to":          r.To,
					"source":      pageFile,
				},
				// Also a graph relation, not only a prop. Every traversal in the
				// system walks Relations — traverse, find_path, impact_analysis,
				// crossrepo — so an edge that lives only in props is invisible to
				// all of them, and `govern` worked purely because it reimplements
				// the join by hand. The props stay: they are what govern and the
				// intentcheck explainer already read.
				Relations: []facts.Relation{{Kind: facts.RelDependsOn, Target: r.To}},
			})
		}
		for _, a := range pg.Anchors {
			out = append(out, facts.Fact{
				Kind: facts.KindIntent,
				Name: fmt.Sprintf("anchor: %s %s", a.Repo, a.Path),
				File: pageFile,
				Props: map[string]any{
					"intent_kind":  "anchor",
					"intent_owner": a.Repo,
					"path":         a.Path,
					"source":       pageFile,
				},
				// Repo-qualified, because that is how a file is named everywhere
				// else in a union and an unqualified path would bind to whichever
				// repository happened to have one by that name.
				Relations: []facts.Relation{{
					Kind:   facts.RelDependsOn,
					Target: a.Repo + "/" + a.Path,
				}},
			})
		}
	}
	for _, c := range p.Consumes {
		out = append(out, facts.Fact{
			Kind: facts.KindIntent,
			Name: fmt.Sprintf("%s consumes %s via %s", c.Repo, c.Target, c.Via),
			File: pageFile,
			Props: map[string]any{
				"intent_kind":  "consumes",
				"intent_owner": c.Repo,
				"target":       c.Target,
				"via":          c.Via,
				"source":       pageFile,
			},
		})
	}
	for _, l := range p.Layers {
		for i, en := range l.Order {
			out = append(out, facts.Fact{
				Kind: facts.KindIntent,
				Name: fmt.Sprintf("%s layer %s", l.Repo, en.Name),
				File: pageFile,
				Props: map[string]any{
					"intent_kind":  "layer",
					"intent_owner": l.Repo,
					"layer_name":   en.Name,
					"order":        i,
					"paths":        append([]string(nil), en.Paths...),
					"source":       pageFile,
				},
			})
		}
	}
	for _, c := range p.Claims {
		props := map[string]any{
			"intent_kind": "claim",
			"metric":      c.Metric,
			"source":      pageFile,
		}
		name := ""
		switch c.Metric {
		case "fact-count":
			props["intent_owner"] = c.Repo
			props["fact_kind"] = c.Kind
			if c.FilePrefix != "" {
				props["file_prefix"] = c.FilePrefix
			}
			if c.NamePrefix != "" {
				props["name_prefix"] = c.NamePrefix
			}
			props["value"] = *c.Value
			// The prefixes are optional, so they are appended only when present:
			// interpolating them unconditionally left "claim: api route  = 99",
			// and that name is what a failed-claim finding is titled with.
			name = fmt.Sprintf("claim: %s %s", c.Repo, c.Kind)
			if scope := strings.TrimSpace(c.FilePrefix + " " + c.NamePrefix); scope != "" {
				name += " " + scope
			}
			name += fmt.Sprintf(" = %d", *c.Value)
		case "seam":
			props["intent_owner"] = c.Consumer
			props["provider"] = c.Provider
			props["via"] = c.Via
			name = fmt.Sprintf("claim: seam %s -> %s via %s", c.Consumer, c.Provider, c.Via)
		}
		out = append(out, facts.Fact{Kind: facts.KindIntent, Name: name, File: pageFile, Props: props})
	}
	return out
}

// edgeFormProps writes the props of the forms whose verdict is a claim about edges; one form selects per rule, so at most one case fires.
func edgeFormProps(r ConstraintRule, extra map[string]any) {
	switch {
	case r.Forbid != "":
		extra["forbid"] = r.Forbid
		extra["via"] = r.Via
		if len(r.ToName) > 0 {
			targets := append([]string(nil), r.ToName...)
			sort.Strings(targets)
			extra["to_name"] = strings.Join(targets, " ")
			if r.Receiver != "" {
				extra["receiver"] = r.Receiver
			}
		} else {
			extra["to"] = r.To
		}
	case r.ForbidReach != "":
		extra["forbid_reach"] = r.ForbidReach
		extra["to"] = r.To
		// An absent via is the whole rule-via vocabulary at verdict time, so
		// only a declared narrowing fingerprints.
		if r.Via != "" {
			extra["via"] = r.Via
		}
	case r.Allow != "":
		// Sorted for the same reason a component's match patterns are: the
		// compiled fact is a function of the declared SET.
		only := append([]string(nil), r.Only...)
		sort.Strings(only)
		extra["allow"] = r.Allow
		extra["only"] = strings.Join(only, " ")
		extra["via"] = r.Via
	case r.Protect != "":
		owners := append([]string(nil), r.Owners...)
		sort.Strings(owners)
		extra["protect"] = r.Protect
		extra["owners"] = strings.Join(owners, " ")
		extra["via"] = r.Via
	case r.Private != "":
		extra["private"] = r.Private
		if len(r.Except) > 0 {
			except := append([]string(nil), r.Except...)
			sort.Strings(except)
			extra["except"] = strings.Join(except, " ")
		}
	case r.RequireEdge != "":
		extra["require_edge"] = r.RequireEdge
		extra["direction"] = r.Direction
		extra["via"] = r.Via
		if r.To != "" {
			extra["to"] = r.To
		}
		if len(r.WhenEdgeTo) > 0 {
			targets := append([]string(nil), r.WhenEdgeTo...)
			sort.Strings(targets)
			extra["when_edge_to"] = strings.Join(targets, " ")
			extra["when_via"] = r.WhenVia
		}
	case r.Protocol != "":
		extra["protocol"] = r.Protocol
		extra["steps"] = strings.Join(r.Steps, " ")
		extra["via"] = r.Via
		extra["verification"] = "structural"
	}
}

// memberFormProps writes the props of the forms that read a member's own facts: emptiness, caps, defined methods, cycles among parts, includer independence and the property form.
func memberFormProps(r ConstraintRule, extra map[string]any) {
	switch {
	case r.ForbidFact != "":
		extra["forbid_fact"] = r.ForbidFact
	case r.Cap != "":
		extra["cap"] = r.Cap
		extra["max_members"] = r.MaxMembers
	case r.RequireDefines != "":
		extra["require_defines"] = r.RequireDefines
		extra["method"] = r.Method
		if len(r.AnyOf) > 0 {
			extra["any_of"] = strings.Join(r.AnyOf, " ")
		}
	case r.ForbidCycles != "":
		extra["forbid_cycles"] = r.ForbidCycles
		extra["among"] = strings.Join(r.Among, " ")
	case r.Independent != "":
		extra["independent"] = r.Independent
	case r.Require != "":
		extra["require"] = r.Require
		extra["must_prop"] = r.MustPropContain.Prop
		extra["must_value"] = r.MustPropContain.Value
		if r.WhenPropContains != nil {
			extra["when_prop"] = r.WhenPropContains.Prop
			extra["when_value"] = r.WhenPropContains.Value
		}
		if len(r.WhenEdgeTo) > 0 {
			targets := append([]string(nil), r.WhenEdgeTo...)
			sort.Strings(targets)
			extra["when_edge_to"] = strings.Join(targets, " ")
			extra["via"] = r.Via
		}
	}
}

// namingAndGuidanceProps writes the props of the naming forms and the guidance form.
func namingAndGuidanceProps(r ConstraintRule, extra map[string]any) {
	switch {
	case r.RequireName != "":
		extra["require_name"] = r.RequireName
		extra["pattern"] = r.Pattern
		if r.Requires != "" {
			extra["requires"] = r.Requires
		}
	case r.ForbidName != "":
		extra["forbid_name"] = r.ForbidName
		extra["pattern"] = r.Pattern
		if r.Surface != "" {
			extra["surface"] = r.Surface
		}
	case r.Guide != "":
		extra["guide"] = r.Guide
		extra["message"] = r.Message
		if len(r.Exemplars) > 0 {
			exemplars := append([]string(nil), r.Exemplars...)
			sort.Strings(exemplars)
			extra["exemplars"] = strings.Join(exemplars, " ")
		}
	}
}
