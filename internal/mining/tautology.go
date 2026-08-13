package mining

import (
	"sort"
	"strings"
)

func tautologicalImplication(antecedent, consequent propPair) bool {
	return antecedent.prop == consequent.prop || antecedent.value == consequent.value
}

func constructionSatisfiedPattern(cluster, pattern string) bool {
	literal, isPrefix := strings.CutSuffix(pattern, "*")
	if !isPrefix || strings.HasPrefix(literal, "*") {
		return false
	}
	return strings.HasPrefix(cluster+"/", literal)
}

func tautologicalNaming(cluster, pattern string, memberCount int) bool {
	return memberCount == 1 || constructionSatisfiedPattern(cluster, pattern)
}

func reversedPairKey(antecedent, consequent propPair) string {
	first, second := antecedent, consequent
	if second.prop < first.prop || (second.prop == first.prop && second.value < first.value) {
		first, second = second, first
	}
	return identityKey(first.prop, first.value, second.prop, second.value)
}

func (s *scope) dropReversedDuplicates(candidates []Candidate) []Candidate {
	byPair := map[string][]int{}
	for i, c := range candidates {
		if c.Confidence == 1.0 {
			byPair[c.pairKey] = append(byPair[c.pairKey], i)
		}
	}
	drop := map[int]bool{}
	for _, key := range sortedKeys(byPair) {
		indices := byPair[key]
		if len(indices) < 2 {
			continue
		}
		sort.SliceStable(indices, func(a, b int) bool {
			ca, cb := candidates[indices[a]], candidates[indices[b]]
			if ca.Denominator != cb.Denominator {
				return ca.Denominator > cb.Denominator
			}
			return ca.Identity < cb.Identity
		})
		for _, i := range indices[1:] {
			drop[i] = true
			s.suppressed[FamilyPropImplication].Tautological++
		}
	}
	kept := make([]Candidate, 0, len(candidates))
	for i, c := range candidates {
		if !drop[i] {
			kept = append(kept, c)
		}
	}
	return kept
}
