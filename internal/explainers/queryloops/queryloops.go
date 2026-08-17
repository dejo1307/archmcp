// Package queryloops reports a database query issued once per iteration of a
// data-sized loop.
//
// It reports the shape it can prove and deliberately not the one everybody
// means by "N+1". The pitched detector was `record.association` inside a loop,
// and the funnel on a large Rails monolith ends at zero: 1,698 association-name
// reads in unbounded loops, 62 once bare `self.assoc` reads are dropped (Rails
// memoises those), 8 once thread-local receivers like `Current.company` are
// dropped, 7 after eager-loading — and all seven false.
// `Requisition#send_pusher_event!` reads `channel.trigger` where `channel` is a
// `PusherChannel`, matched only because `belongs_to :trigger` exists on an
// unrelated model. The graph has no receiver type inference for Ruby, so
// `candidate.posts` and `client.post` are the same string to it.
//
// A class-level query has no such problem: the receiver IS the type. When
// `AccessLevel.find_by` appears inside an unbounded loop, `AccessLevel` is a
// model this graph already knows and `find_by` is a query — nothing is
// inferred. 97 of these on that monolith.
//
// It also needs no eager-load suppression, and that is a property of the shape
// rather than a shortcut: `includes` cannot help a class-level `find_by`.
package queryloops

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

type Explainer struct{}

func New() *Explainer { return &Explainer{} }

func (e *Explainer) Name() string { return "query-loops" }

// queryMethods are ActiveRecord class methods that reach the database. A class
// method that does not is not a finding — `AccessLevel.human_name` is free.
var queryMethods = map[string]bool{
	"find": true, "find_by": true, "find_by!": true, "find_or_create_by": true,
	"find_or_create_by!": true, "find_or_initialize_by": true, "where": true,
	"first": true, "last": true, "count": true, "exists?": true, "pluck": true,
	"sum": true, "create": true, "create!": true, "update": true, "update!": true,
	"destroy": true, "destroy_all": true, "save": true, "save!": true,
	"order": true, "find_each": true, "take": true, "average": true, "minimum": true,
	"maximum": true, "delete_all": true, "upsert": true, "insert": true,
}

type finding struct {
	symbol string
	file   string
	repo   string
	call   string
	depth  int
	// element and target are set only for the association-read shape: the type
	// the block parameter holds, and what the association it reads points at.
	element string
	target  string
	// write marks a persistence call rather than a read: it cannot be
	// eager-loaded away, so it needs a different fix and says so.
	write bool
}

// persistMethods write to the database on an instance. Kept apart from
// queryMethods, which are class-level and include reads.
var persistMethods = map[string]bool{
	"save": true, "save!": true, "update": true, "update!": true,
	"destroy": true, "delete": true, "touch": true, "increment!": true,
	"decrement!": true, "update_column": true, "update_columns": true,
}

// associationIndex answers the two questions the association-read shape needs:
// what a collection's elements are, and whether a method is an association on
// that type. Both come from association facts the graph already holds.
type associationIndex struct {
	byName map[string][]string
	onType map[string]map[string]string
}

// targetsOf returns the distinct types a collection name resolves to. Distinct,
// because the same association declared identically on several models is not
// ambiguity — `has_many :users` pointing at User from twenty models still says
// the element is a User.
func (i associationIndex) targetsOf(collection string) []string {
	seen := map[string]bool{}
	var out []string
	for _, target := range i.byName[collection] {
		if seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}

func (i associationIndex) on(model, method string) string {
	if byModel, ok := i.onType[model]; ok {
		return byModel[method]
	}
	return ""
}

func (e *Explainer) Explain(ctx context.Context, store *facts.Store) ([]facts.Insight, error) {
	// Two shapes, two prerequisites. The class-level shape needs the model
	// classes; the association-read shape needs association facts and block
	// bindings. Gating both on models would have silenced the association shape
	// wherever storage facts were absent, which is a repository that has
	// associations and no tables — rare, and exactly the kind of silent zero
	// this explainer exists downstream of.
	models := modelClasses(store)
	associations := buildAssociationIndex(store)
	if len(models) == 0 && len(associations.byName) == 0 {
		return nil, nil
	}

	var found []finding
	for _, fact := range store.ByKind(facts.KindSymbol) {
		// The association-read shape: an association read on the element of a
		// collection. `form_questions.each { |q| q.form_answers }` is a query per
		// iteration only because `q` is a FormQuestion, and the block binding is
		// what says so. Without it the receiver is untyped and `client.post`
		// matches an association named `post` on an unrelated model — which is
		// exactly how the first attempt produced seven candidates and zero true
		// findings.
		// A local typed by assignment resolves the same way a block parameter
		// does, and gives the same guarantee: the receiver's type is stated in
		// the source rather than guessed from a name.
		for _, typed := range propStrings(fact.Props["local_types"]) {
			name, class, ok := strings.Cut(typed, "=")
			if !ok {
				continue
			}
			for _, call := range propStrings(fact.Props["calls_in_loop"]) {
				receiver, method, dotted := strings.Cut(call, ".")
				if !dotted || receiver != name {
					continue
				}
				// A write per iteration is worse than a read per iteration: it
				// cannot be eager-loaded away, and each one is a round trip that
				// a single update_all or insert_all would replace. Reported only
				// on a typed receiver, so `client.save` is not mistaken for a
				// record being persisted.
				if models[demodulize(class)] && persistMethods[method] {
					found = append(found, finding{
						symbol: fact.Name, file: fact.File, repo: fact.Repo,
						call: call, depth: propInt(fact.Props["loop_depth"]),
						element: demodulize(class), write: true,
					})
					continue
				}
				if target := associations.on(demodulize(class), method); target != "" {
					found = append(found, finding{
						symbol: fact.Name, file: fact.File, repo: fact.Repo,
						call: call, depth: propInt(fact.Props["loop_depth"]),
						element: demodulize(class), target: target,
					})
				}
			}
		}
		for _, binding := range propStrings(fact.Props["block_bindings"]) {
			param, collection, ok := strings.Cut(binding, "=")
			if !ok {
				continue
			}
			// A collection name declared on more than one model gives the element
			// more than one candidate type, and picking one would be the
			// name-coincidence guess that produced seven false findings before.
			// A missing edge beats a wrong one.
			elements := associations.targetsOf(collection)
			if len(elements) != 1 {
				continue
			}
			for _, element := range elements {
				for _, call := range propStrings(fact.Props["calls_in_loop"]) {
					receiver, method, dotted := strings.Cut(call, ".")
					if !dotted || receiver != param {
						continue
					}
					if target := associations.on(element, method); target != "" {
						found = append(found, finding{
							symbol: fact.Name, file: fact.File, repo: fact.Repo,
							call: call, depth: propInt(fact.Props["loop_depth"]),
							element: element, target: target,
						})
					}
				}
			}
		}
	}
	for _, fact := range store.ByKind(facts.KindSymbol) {
		if lang, _ := fact.Props["language"].(string); lang != "ruby" {
			continue
		}
		if len(models) == 0 {
			continue
		}
		depth := propInt(fact.Props["loop_depth"])
		for _, call := range propStrings(fact.Props["calls_in_loop"]) {
			receiver, method, ok := strings.Cut(call, ".")
			if !ok || !queryMethods[method] {
				continue
			}
			if !models[demodulize(receiver)] {
				continue
			}
			found = append(found, finding{
				symbol: fact.Name, file: fact.File, repo: fact.Repo,
				call: call, depth: depth,
			})
		}
	}
	if len(found) == 0 {
		return nil, nil
	}
	// One finding per (symbol, call): a loop body reporting the same read twice
	// is the explainer counting its own passes, not two problems.
	deduped := found[:0]
	seenFinding := map[string]bool{}
	for _, f := range found {
		key := f.symbol + "\x00" + f.call
		if seenFinding[key] {
			continue
		}
		seenFinding[key] = true
		deduped = append(deduped, f)
	}
	found = deduped

	// Deeper loops first: a query at depth 2 runs a product of two collections,
	// which is a different order of problem from one at depth 1.
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].depth != found[j].depth {
			return found[i].depth > found[j].depth
		}
		return found[i].symbol < found[j].symbol
	})

	out := make([]facts.Insight, 0, len(found))
	for _, f := range found {
		description := fmt.Sprintf(
			"%s calls %s inside a loop nested %d deep. The receiver is a model this "+
				"graph knows and the method reaches the database, so the query count "+
				"grows with the collection. Eager loading cannot help a class-level "+
				"query — the fix is to read the set once outside the loop.",
			f.symbol, f.call, f.depth)
		actions := []string{
			"read the records once before the loop and index them in memory",
			"confirm with a query-count test that fails against the current code",
		}
		if f.write {
			description = fmt.Sprintf(
				"%s calls %s inside a loop nested %d deep, on a %s. A write per "+
					"iteration is a round trip per element and cannot be eager-loaded "+
					"away — the fix is a single bulk update rather than a preload.",
				f.symbol, f.call, f.depth, f.element)
			actions = []string{
				"replace the per-element write with update_all or insert_all",
				"confirm with a query-count test that fails against the current code",
			}
		} else if f.element != "" {
			// The association-read shape names its own fix, because eager loading
			// is exactly what solves it — unlike the class-level shape, where
			// `includes` cannot help.
			description = fmt.Sprintf(
				"%s reads %s inside a loop nested %d deep. The block parameter holds a %s, "+
					"so each iteration loads its %s — a query per element. Unlike a "+
					"class-level query this one eager-loads away.",
				f.symbol, f.call, f.depth, f.element, f.target)
			actions = []string{
				fmt.Sprintf("eager-load the association on the collection: includes(:%s)",
					strings.SplitN(f.call, ".", 2)[1]),
				"confirm with a query-count test that fails against the current code",
			}
		}
		out = append(out, facts.Insight{
			Title:       fmt.Sprintf("%s issues %s once per iteration", f.symbol, f.call),
			Description: description,
			// Below 1.0 and deliberately so: the loop is measured, the receiver is
			// measured, and whether the loop is hot is not. This is a candidate to
			// verify against a query count, which is the one oracle available here.
			Confidence: 0.8,
			Evidence: []facts.Evidence{{
				File:   f.file,
				Symbol: f.symbol,
				Detail: fmt.Sprintf("%s at loop depth %d", f.call, f.depth),
			}},
			Actions: actions,
		})
	}
	return out, nil
}

// modelClasses is the set of class names this graph knows to be models, keyed
// without namespace. Being able to answer that question is the entire reason
// this explainer works where the association-read one did not.
func modelClasses(store *facts.Store) map[string]bool {
	models := map[string]bool{}
	for _, fact := range store.ByKind(facts.KindStorage) {
		if kind, _ := fact.Props["storage_kind"].(string); kind != "model" {
			continue
		}
		models[demodulize(fact.Name)] = true
	}
	return models
}

func demodulize(name string) string {
	if i := strings.LastIndex(name, "::"); i >= 0 {
		return name[i+2:]
	}
	return name
}

func propStrings(v any) []string {
	switch value := v.(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func propInt(v any) int {
	switch value := v.(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	}
	return 0
}

func buildAssociationIndex(store *facts.Store) associationIndex {
	index := associationIndex{byName: map[string][]string{}, onType: map[string]map[string]string{}}
	for _, fact := range store.ByKind(facts.KindAssociation) {
		name, _ := fact.Props["association"].(string)
		model, _ := fact.Props["model"].(string)
		target, _ := fact.Props["target"].(string)
		if name == "" || target == "" {
			continue
		}
		index.byName[name] = append(index.byName[name], target)
		if index.onType[model] == nil {
			index.onType[model] = map[string]string{}
		}
		index.onType[model][name] = target
	}
	return index
}
