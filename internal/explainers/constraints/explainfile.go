package constraints

import (
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// FileMembership is one component a file's facts belong to, with the selector
// that admitted them and the members it holds there.
type FileMembership struct {
	Component string   `json:"component"`
	Selector  string   `json:"selector"`
	Source    string   `json:"source"`
	Members   []string `json:"members"`
}

// OutgoingEdge is one relation a fact in the file makes.
type OutgoingEdge struct {
	From   string `json:"from"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Line   int    `json:"line"`
}

// FileExplanation answers "why does this file belong where it belongs": the
// components whose selectors admit a fact in the file, with the selector
// stated, and the edges those facts make. It reads the same membership the
// evaluator reads, so the sentence and the verdict cannot disagree.
type FileExplanation struct {
	File        string           `json:"file"`
	Facts       []string         `json:"facts"`
	Memberships []FileMembership `json:"memberships"`
	Outgoing    []OutgoingEdge   `json:"outgoing"`
}

// ExplainFile reads a file's membership and edges off the snapshot.
func ExplainFile(store *facts.Store, file string) FileExplanation {
	out := FileExplanation{File: file}
	file = strings.TrimPrefix(file, "./")
	inFile := func(f facts.Fact) bool {
		return f.File == file || strings.TrimPrefix(f.File, f.Repo+"/") == file
	}
	for _, f := range store.All() {
		if f.Kind == facts.KindIntent || !inFile(f) {
			continue
		}
		out.Facts = append(out.Facts, f.Kind+" "+f.Name)
		for _, rel := range f.Relations {
			if rel.Kind == facts.RelDeclares {
				continue
			}
			out.Outgoing = append(out.Outgoing, OutgoingEdge{From: f.Name, Kind: rel.Kind, Target: rel.Target, Line: f.Line})
		}
	}
	sort.Strings(out.Facts)
	sort.Slice(out.Outgoing, func(i, j int) bool {
		if out.Outgoing[i].Line != out.Outgoing[j].Line {
			return out.Outgoing[i].Line < out.Outgoing[j].Line
		}
		return out.Outgoing[i].Target < out.Outgoing[j].Target
	})
	components, _ := declarations(store)
	names := make([]string, 0, len(components))
	for name := range components {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := components[name]
		_, members := resolveMembership(store, c)
		var here []string
		for _, m := range members {
			if inFile(m) {
				here = append(here, m.Name)
			}
		}
		if len(here) == 0 {
			continue
		}
		sort.Strings(here)
		out.Memberships = append(out.Memberships, FileMembership{Component: name, Selector: selectorSummary(c), Source: c.source, Members: here})
	}
	return out
}
