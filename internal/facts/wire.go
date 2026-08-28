package facts

// The wire shape of facts.jsonl.
//
// It differs from Fact and Relation by exactly the two id fields, and it exists
// as a separate type so those never reach the in-memory model. Embedding rather
// than restating the fields is what makes that safe: a field added to Fact
// appears here automatically, in the same position, so the two cannot drift.
//
// FIELD ORDER MATTERS. encoding/json emits fields in declaration order, so ID
// last keeps every line's prefix byte-identical to what it was before ids
// existed. WriteJSONL sorts the marshalled LINES, so a leading id would have
// re-ordered the whole file — and pkg/history stores each revision as a patch
// over those sorted lines, so the reordering would have cost a full rewrite of
// every stored revision rather than the one fresh base this actually costs.

// wireRelation is a Relation plus the identity of the fact its target names,
// when that name resolves to exactly one.
type wireRelation struct {
	Relation
	// TargetID is omitted rather than emptied when the target does not resolve:
	// most unresolved targets are stdlib or third-party names with no fact in
	// the snapshot at all (42.8% of relations in a measured corpus), and an
	// empty string would present "nothing to point at" and "I could not decide"
	// as the same answer. Absent means "resolve this by name yourself", which is
	// what every consumer already does today.
	TargetID string `json:"target_id,omitempty"`
}

// wireFact is a Fact plus its identity, with its relations widened to carry
// theirs.
type wireFact struct {
	Fact
	// Relations shadows Fact.Relations: a field at depth 0 wins over a promoted
	// one at depth 1, so this is what marshals, under the same key.
	Relations []wireRelation `json:"relations,omitempty"`
	ID        string         `json:"id"`
}

// targetFactFor resolves a relation target NAME to the index of the fact it
// names, or -1 when the snapshot cannot answer that unambiguously. The caller
// turns the index into an id, so resolution and hashing stay separable and the
// hash can reuse one scratch buffer for the whole pass.
//
// Two ways it declines. The name may match no fact — the common case, and not a
// defect: an edge to fmt.Sprintf or to a third-party package names something
// this repository does not contain. Or it may match facts of more than one
// identity, and there is no honest way to choose; enola's own graph does not
// choose either (it is name-keyed, and reports the residual as Conflated), so
// emitting a pick here would invent a precision the analysis does not have.
//
// Facts in the caller's own repository win over facts elsewhere in a multi-repo
// snapshot, matching how a consumer already resolves these by hand. The result
// does not depend on the order of the byName bucket: an id is returned only when
// every candidate agrees on it.
//
// The caller must hold s.mu.
func (s *Store) targetFactFor(target, fromRepo string) int {
	idx := s.byName[target]
	if len(idx) == 0 {
		return -1
	}

	pick := -1
	for _, i := range idx {
		if s.facts[i].Repo != fromRepo {
			continue
		}
		if pick == -1 {
			pick = i
			continue
		}
		if !sameIdentity(s.facts[pick], s.facts[i]) {
			return -1 // ambiguous inside the repository that made the reference
		}
	}

	if pick == -1 {
		// No fact in this repository carries the name, so the reference points
		// outward: consider the whole snapshot, under the same all-or-nothing rule.
		pick = idx[0]
		for _, i := range idx[1:] {
			if !sameIdentity(s.facts[pick], s.facts[i]) {
				return -1
			}
		}
	}

	return pick
}
