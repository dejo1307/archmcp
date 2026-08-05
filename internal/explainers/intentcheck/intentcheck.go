// Package intentcheck verdicts declared intent against the measured graph —
// the compile step's consumer. It reads only the fact store, like every
// explainer: declarations arrive as KindIntent facts (the compilation the
// engine performs at snapshot time), measured seams as the cross-repo
// dependency facts the linker draws.
//
// Verdicts are per-repo opt-in: a repo with no consumes declarations gets no
// seam verdicts (undeclared is unasked), and a declared seam whose target
// repo is absent from the graph is skipped, never failed — no counterparty
// means no claim is possible. Confidences are honest under the explainer
// doctrine: a mismatch that is pure set difference between stated and
// measured is proof-class (1.0); a missing seam is capped at 0.8, because
// the absent edge can be architectural drift or an extraction miss, and an
// estimate must never present as a certainty.
package intentcheck

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// Explainer verdicts intent facts against measured cross-repo edges.
type Explainer struct{}

// New creates the explainer.
func New() *Explainer { return &Explainer{} }

// Name returns the explainer identifier; `check --fail-on=intent` selects it
// by this name.
func (e *Explainer) Name() string { return "intent" }

// missingSeamConfidence caps the one estimating verdict: an absent measured
// edge may be drift or an extraction miss.
const missingSeamConfidence = 0.8

type declaredSeam struct {
	target, via string
	source      string
}

type measuredEdge struct {
	target string
	vias   []string
	name   string
}

// Explain computes seam verdicts and override notices.
func (e *Explainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	declared := map[string][]declaredSeam{}
	var overridden []facts.Fact
	present := map[string]bool{}
	hasIntent := false

	for _, f := range store.All() {
		if f.Repo != "" {
			present[f.Repo] = true
		}
		if f.Kind != facts.KindIntent {
			continue
		}
		hasIntent = true
		if f.PropString("overridden") != "" || f.Props["overridden"] == true {
			overridden = append(overridden, f)
		}
		if f.PropString("intent_kind") != "consumes" {
			continue
		}
		owner := f.PropString("intent_owner")
		if owner == "" {
			owner = f.Repo
		}
		declared[owner] = append(declared[owner], declaredSeam{
			target: f.PropString("target"),
			via:    f.PropString("via"),
			source: f.PropString("source"),
		})
	}
	if !hasIntent {
		return nil, nil
	}

	measured := map[string][]measuredEdge{}
	for _, f := range store.All() {
		if f.Kind != facts.KindDependency || f.PropString("type") != "cross_repo" {
			continue
		}
		parts := strings.Split(f.Name, " -> ")
		if len(parts) != 2 {
			continue
		}
		vias, _ := f.Props["via"].([]string)
		if vias == nil {
			if raw, ok := f.Props["via"].([]any); ok {
				for _, v := range raw {
					if sv, ok := v.(string); ok {
						vias = append(vias, sv)
					}
				}
			}
		}
		measured[f.Repo] = append(measured[f.Repo], measuredEdge{target: parts[1], vias: vias, name: f.Name})
	}

	var insights []facts.Insight
	repos := make([]string, 0, len(declared))
	for r := range declared {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	for _, repo := range repos {
		seams := declared[repo]
		declaredSet := map[string]bool{}
		declaredTargets := map[string]bool{}
		for _, s := range seams {
			declaredSet[s.target+"\x00"+s.via] = true
			declaredTargets[s.target] = true
		}
		measuredByTarget := map[string]map[string]bool{}
		for _, m := range measured[repo] {
			if measuredByTarget[m.target] == nil {
				measuredByTarget[m.target] = map[string]bool{}
			}
			for _, v := range m.vias {
				measuredByTarget[m.target][v] = true
			}
		}

		for _, m := range measured[repo] {
			for _, via := range m.vias {
				if declaredSet[m.target+"\x00"+via] {
					continue
				}
				if declaredTargets[m.target] {
					insights = append(insights, facts.Insight{
						Title:       fmt.Sprintf("Intent mis-via: %s reaches %s via %s", repo, m.target, via),
						Description: fmt.Sprintf("%s declares a seam to %s, but not via %q — the measured edge uses a mechanism the declaration does not name. Correct the declaration or the code; the mismatch itself is exact.", repo, m.target, via),
						Confidence:  1.0,
						Evidence:    []facts.Evidence{{Fact: m.name, Detail: fmt.Sprintf("measured via %s; declared vias for %s do not include it", via, m.target)}},
						Actions:     []string{"Add the via to the declaration if intended", "Remove or migrate the call sites if not"},
					})
				} else {
					insights = append(insights, facts.Insight{
						Title:       fmt.Sprintf("Unexpected seam: %s -> %s via %s", repo, m.target, via),
						Description: fmt.Sprintf("%s declares its consumed seams, and %s is not among them — the graph measures an edge the declaration does not admit. The mismatch is set difference between stated and measured: exact.", repo, m.target),
						Confidence:  1.0,
						Evidence:    []facts.Evidence{{Fact: m.name, Detail: "measured cross-repo edge with no matching declaration"}},
						Actions:     []string{"Declare the seam if it is intended", "Remove the dependency if it is not"},
					})
				}
			}
		}

		for _, s := range seams {
			if !present[s.target] {
				continue // no counterparty in this graph: not asked, never failed
			}
			if measuredByTarget[s.target][s.via] {
				continue
			}
			if declaredTargets[s.target] && len(measuredByTarget[s.target]) > 0 {
				continue // reaches the target by another via: reported above as mis-via
			}
			insights = append(insights, facts.Insight{
				Title:       fmt.Sprintf("Missing intended seam: %s -> %s via %s", repo, s.target, s.via),
				Description: fmt.Sprintf("%s declares it consumes %s via %s, and the graph measures no such edge. Either the architecture drifted from the declaration, or the call is dynamic and extraction cannot see it — this finding cannot tell which, so its confidence is capped.", repo, s.target, s.via),
				Confidence:  missingSeamConfidence,
				Evidence:    []facts.Evidence{{Fact: fmt.Sprintf("consumes %s via %s", s.target, s.via), Detail: "declared in " + s.source}},
				Actions:     []string{"Remove the declaration if the seam is gone", "Record an extraction-gap verdict if the call is dynamic"},
			})
		}
	}

	insights = append(insights, claimVerdicts(store)...)
	insights = append(insights, relationVerdicts(store)...)

	sort.Slice(overridden, func(i, j int) bool {
		if overridden[i].Repo != overridden[j].Repo {
			return overridden[i].Repo < overridden[j].Repo
		}
		return overridden[i].Name < overridden[j].Name
	})
	seenOverride := map[string]bool{}
	for _, f := range overridden {
		if seenOverride[f.Repo] {
			continue
		}
		seenOverride[f.Repo] = true
		insights = append(insights, facts.Insight{
			Title:       fmt.Sprintf("Intent override: cluster config replaces %s's own declaration", f.Repo),
			Description: fmt.Sprintf("%s carries its own %s, and the cluster config's intent entry overrides it wholesale for this run — the repo file was not consulted. Informational: an override must never be only in a log.", f.Repo, "enola-intent.yaml"),
			Confidence:  1.0,
			Evidence:    []facts.Evidence{{Fact: f.Name, Detail: "declaration source: cluster config (overriding repo file)"}},
			Actions:     []string{"Delete the cluster entry when the repo file should own its declaration"},
		})
	}

	return insights, nil
}

// claimVerdicts evaluates claim-kind intent facts against the store: exact
// counting for fact-count, edge existence for seam. Only failures become
// findings (a passing claim is silence, like every agreeing verdict), and a
// failure is proof-class — the claim is stated, the count is counted.
func claimVerdicts(store *facts.Store) []facts.Insight {
	var out []facts.Insight
	all := store.All()
	for _, f := range all {
		if f.Kind != facts.KindIntent || f.PropString("intent_kind") != "claim" {
			continue
		}
		switch f.PropString("metric") {
		case "fact-count":
			owner := f.PropString("intent_owner")
			kind := f.PropString("fact_kind")
			fprefix := f.PropString("file_prefix")
			nprefix := f.PropString("name_prefix")
			want, ok := intPropOf(f, "value")
			if !ok {
				continue
			}
			got := 0
			for _, g := range all {
				if g.Kind != kind || g.Repo != owner {
					continue
				}
				gf := strings.TrimPrefix(g.File, owner+"/")
				if fprefix != "" && !strings.HasPrefix(gf, fprefix) {
					continue
				}
				if nprefix != "" && !strings.HasPrefix(g.Name, nprefix) {
					continue
				}
				got++
			}
			if got != want {
				out = append(out, facts.Insight{
					Title:       fmt.Sprintf("Claim failed: %s", f.Name),
					Description: fmt.Sprintf("The page claims %d and the graph measures %d. Either the architecture moved or the claim is stale — the mismatch itself is exact.", want, got),
					Confidence:  1.0,
					Evidence:    []facts.Evidence{{Fact: f.Name, Detail: "claimed in " + f.PropString("source")}},
					Actions:     []string{"Re-measure and update the claim", "Investigate the movement if the claim was current"},
				})
			}
		case "seam":
			owner := f.PropString("intent_owner")
			provider := f.PropString("provider")
			via := f.PropString("via")
			found := false
			for _, g := range all {
				if g.Kind == facts.KindDependency && g.PropString("type") == "cross_repo" &&
					g.Repo == owner && g.Name == owner+" -> "+provider {
					if vias, ok := g.Props["via"].([]string); ok {
						for _, v := range vias {
							if v == via {
								found = true
							}
						}
					}
				}
			}
			if !found {
				out = append(out, facts.Insight{
					Title:       fmt.Sprintf("Claim failed: %s", f.Name),
					Description: "The claimed seam is not measured in this snapshot.",
					Confidence:  1.0,
					Evidence:    []facts.Evidence{{Fact: f.Name, Detail: "claimed in " + f.PropString("source")}},
					Actions:     []string{"Re-measure; remove or correct the claim if the seam is gone"},
				})
			}
		}
	}
	return out
}

// danglingRelationConfidence caps the knowledge-graph estimate: a relation
// target absent from the compiled set may be a deleted page or a page that
// simply does not opt in — the verdict cannot tell which.
const danglingRelationConfidence = 0.8

// relationVerdicts checks every page relation edge against the compiled page
// set of the same repo. Pages are opt-in, so an absent target is an estimate,
// never a certainty — but in a wiki where every page compiles, a dangling
// edge is a broken link the graph caught.
func relationVerdicts(store *facts.Store) []facts.Insight {
	pages := map[string]bool{}
	var relations []facts.Fact
	for _, f := range store.All() {
		if f.Kind != facts.KindIntent {
			continue
		}
		switch f.PropString("intent_kind") {
		case "page":
			pages[f.Repo+"\x00"+f.File] = true
			pages[f.Repo+"\x00"+strings.TrimPrefix(f.File, f.Repo+"/")] = true
		case "relation":
			relations = append(relations, f)
		}
	}
	var out []facts.Insight
	for _, f := range relations {
		to := f.PropString("to")
		if pages[f.Repo+"\x00"+to] {
			continue
		}
		out = append(out, facts.Insight{
			Title:       fmt.Sprintf("Dangling knowledge relation: %s", f.Name),
			Description: fmt.Sprintf("The page declares a %s relation to %s, and no compiled page exists at that path. Either the target was deleted or moved, or it does not carry a page declaration — this verdict cannot tell which, so its confidence is capped.", f.PropString("rel"), to),
			Confidence:  danglingRelationConfidence,
			Evidence:    []facts.Evidence{{Fact: f.Name, Detail: "declared in " + f.PropString("source")}},
			Actions:     []string{"Fix the path if the target moved", "Remove the relation if the target is gone", "Add a page declaration to the target if it should compile"},
		})
	}
	return out
}

func intPropOf(f facts.Fact, key string) (int, bool) {
	switch v := f.Props[key].(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	}
	return 0, false
}
