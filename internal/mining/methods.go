package mining

import (
	"fmt"
	"sort"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/intent"
)

func (s *scope) mineDefines() []Candidate {
	symbolFacts := s.allFacts[facts.KindSymbol]
	if len(symbolFacts) == 0 {
		return nil
	}
	composed := s.composedNames(symbolFacts)
	definedNames := map[string]bool{}
	for _, m := range symbolFacts {
		definedNames[m.name] = true
	}
	sortedNames := sortedKeys(definedNames)

	plainClass := map[string]bool{}
	for _, m := range symbolFacts {
		if m.fact.PropString("symbol_kind") == facts.SymbolClass {
			if _, seen := plainClass[m.name]; !seen {
				plainClass[m.name] = true
			}
		} else {
			plainClass[m.name] = false
		}
	}

	var eligible []member
	for _, m := range s.members[facts.KindSymbol] {
		if plainClass[m.name] && !composed[m.name] {
			eligible = append(eligible, m)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	methodsOf := make([][]string, len(eligible))
	globalTally := map[string]int{}
	for i, class := range eligible {
		methodsOf[i] = definedMethods(sortedNames, class.name)
		for _, method := range methodsOf[i] {
			globalTally[method]++
		}
	}

	clusters := map[string][]int{}
	for i, class := range eligible {
		for _, cluster := range clusterPrefixes(class.file) {
			clusters[cluster] = append(clusters[cluster], i)
		}
	}

	var out []Candidate
	kept := map[string][]string{}
	for _, cluster := range sortedKeys(clusters) {
		idx := clusters[cluster]
		tally := map[string]int{}
		for _, i := range idx {
			for _, method := range methodsOf[i] {
				tally[method]++
			}
		}
		for _, method := range sortedKeys(tally) {
			confidence := ratio(tally[method], len(idx))
			if confidence < s.cfg.MinConfidence {
				continue
			}
			if confidence <= ratio(globalTally[method], len(eligible)) {
				continue
			}
			if coveredByAncestor(kept, cluster, method) {
				continue
			}
			exceptions := defineExceptions(eligible, idx, methodsOf, method)
			if !s.admit(FamilyMethodPresence, len(idx), len(exceptions)) {
				continue
			}
			kept[method] = append(kept[method], cluster)
			component := s.clusterComponent(cluster, facts.KindSymbol)
			statement := s.statement(fmt.Sprintf("%d/%d plain classes under %s/ define %s", tally[method], len(idx), cluster, method))
			rule := intent.ConstraintRule{
				ID:             slug("mined", cluster, "defines", method),
				RequireDefines: component.Name,
				Method:         method,
				Mode:           "advisory",
				Because:        because(statement, exceptions),
			}
			out = append(out, Candidate{
				Family:      FamilyMethodPresence,
				Kind:        facts.KindSymbol,
				Service:     s.service,
				Identity:    identityKey(FamilyMethodPresence, s.service, cluster, method),
				Statement:   statement,
				Numerator:   tally[method],
				Denominator: len(idx),
				Confidence:  confidence,
				Exceptions:  exceptions,
				Components:  []intent.ConstraintComponent{component},
				Rule:        rule,
			})
		}
	}
	return out
}

func (s *scope) composedNames(symbolFacts []member) map[string]bool {
	composed := map[string]bool{}
	for _, m := range symbolFacts {
		for _, rel := range m.fact.Relations {
			if rel.Kind == facts.RelImplements {
				composed[m.name] = true
			}
		}
	}
	for _, m := range s.allFacts[facts.KindDependency] {
		for _, rel := range m.fact.Relations {
			if rel.Kind != facts.RelImplements {
				continue
			}
			if source, ok := strings.CutSuffix(m.name, " -> "+rel.Target); ok {
				composed[source] = true
			}
		}
	}
	return composed
}

func definedMethods(sortedNames []string, class string) []string {
	seen := map[string]bool{}
	var out []string
	for _, sep := range []string{"#", "."} {
		prefix := class + sep
		at := sort.SearchStrings(sortedNames, prefix)
		for ; at < len(sortedNames) && strings.HasPrefix(sortedNames[at], prefix); at++ {
			method := strings.TrimPrefix(sortedNames[at], prefix)
			if method == "" || strings.ContainsAny(method, " \t") || seen[method] {
				continue
			}
			seen[method] = true
			out = append(out, method)
		}
	}
	sort.Strings(out)
	return out
}

func defineExceptions(eligible []member, idx []int, methodsOf [][]string, method string) []Exception {
	var out []Exception
	for _, i := range idx {
		defined := false
		for _, m := range methodsOf[i] {
			if m == method {
				defined = true
				break
			}
		}
		if defined {
			continue
		}
		out = append(out, Exception{Name: eligible[i].name, File: eligible[i].file, Detail: "no measured definition of " + method})
	}
	return out
}
