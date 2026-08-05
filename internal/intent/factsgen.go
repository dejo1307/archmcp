package intent

import (
	"fmt"

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
	return out
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
			name = fmt.Sprintf("claim: %s %s %s%s = %d", c.Repo, c.Kind, c.FilePrefix, c.NamePrefix, *c.Value)
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
