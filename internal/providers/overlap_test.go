package providers

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// The join's contract: every provider relation lands in exactly one bucket, a
// repeated edge is already_resolved, the same target spelled the provider's
// way is respelled with the pair kept, a second ancestor is a conflict with
// the extractor's kept, a source the extractor never declared is
// no_extractor_symbol, and a call the extractor lacks is provider_only.
func TestOverlap_EveryRelationLandsInOneBucket(t *testing.T) {
	extracted := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "Order", Relations: []facts.Relation{
			{Kind: facts.RelImplements, Target: "ApplicationRecord"},
			{Kind: facts.RelCalls, Target: "Object"},
			{Kind: facts.RelDependsOn, Target: "Invoice"},
		}},
		{Kind: facts.KindSymbol, Name: "Invoice"},
	}
	provider := []facts.Fact{
		{Kind: facts.KindSymbol, Name: "Order", Relations: []facts.Relation{
			{Kind: facts.RelImplements, Target: "ApplicationRecord"},
			{Kind: facts.RelImplements, Target: "Auditable"},
			{Kind: facts.RelCalls, Target: "<Object>"},
			{Kind: facts.RelCalls, Target: "Invoice#total"},
			{Kind: facts.RelDependsOn, Target: "Invoice"},
		}},
		{Kind: facts.KindSymbol, Name: "Ghost", Relations: []facts.Relation{
			{Kind: facts.RelCalls, Target: "Order"},
		}},
	}
	got := Overlap(extracted, provider)

	impl := got[facts.RelImplements]
	if impl.AlreadyResolved != 1 || impl.Conflict != 1 || impl.ProviderOnly != 0 {
		t.Fatalf("implements: %+v", *impl)
	}
	if len(impl.Conflicts) != 1 || impl.Conflicts[0].Provider != "Auditable" || impl.Conflicts[0].Extractor != "ApplicationRecord" {
		t.Fatalf("conflict evidence: %+v", impl.Conflicts)
	}
	calls := got[facts.RelCalls]
	if calls.Respelled != 1 || calls.ProviderOnly != 1 || calls.NoExtractorSymbol != 1 || calls.Conflict != 0 {
		t.Fatalf("calls: %+v", *calls)
	}
	if len(calls.Respellings) != 1 || calls.Respellings[0].Provider != "<Object>" || calls.Respellings[0].Extractor != "Object" {
		t.Fatalf("respelling evidence: %+v", calls.Respellings)
	}
	dep := got[facts.RelDependsOn]
	if dep.AlreadyResolved != 1 || dep.Conflict != 0 {
		t.Fatalf("depends_on: %+v", *dep)
	}
	total := 0
	for _, o := range got {
		total += o.AlreadyResolved + o.Respelled + o.Conflict + o.NoExtractorSymbol + o.ProviderOnly
	}
	if total != 6 {
		t.Fatalf("six provider relations, %d bucketed", total)
	}
}

// A provider states an edge as a dependency fact named "<tag>: <source> ->
// <target>"; the join reads the source out of that name.
func TestOverlap_ReadsTheSourceFromADependencyFactName(t *testing.T) {
	extracted := []facts.Fact{{Kind: facts.KindSymbol, Name: "Company#account", Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Account.find_by"}}}}
	provider := []facts.Fact{
		{Kind: facts.KindDependency, Name: "prism-call: Company#account -> Account.find_by", Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Account.find_by"}}},
		{Kind: facts.KindDependency, Name: "prism-call: Company#account -> Account#find_by", Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Account#find_by"}}},
		{Kind: facts.KindDependency, Name: "prism-call: Company#account -> Current", Relations: []facts.Relation{{Kind: facts.RelCalls, Target: "Current"}}},
	}
	got := Overlap(extracted, provider)[facts.RelCalls]
	if got.AlreadyResolved != 1 || got.Respelled != 1 || got.ProviderOnly != 1 || got.NoExtractorSymbol != 0 {
		t.Fatalf("calls: %+v", *got)
	}
}

func TestOverlap_EvidenceIsCapped(t *testing.T) {
	var extracted, provider []facts.Fact
	for i := 0; i < overlapEvidenceCap+5; i++ {
		name := string(rune('A' + i))
		extracted = append(extracted, facts.Fact{Kind: facts.KindSymbol, Name: name, Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "Base"}}})
		provider = append(provider, facts.Fact{Kind: facts.KindSymbol, Name: name, Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "Other"}}})
	}
	got := Overlap(extracted, provider)[facts.RelImplements]
	if got.Conflict != overlapEvidenceCap+5 || len(got.Conflicts) != overlapEvidenceCap {
		t.Fatalf("conflict %d, evidence %d", got.Conflict, len(got.Conflicts))
	}
}

// Account stamps which tree the facts describe: a provider that wrote its
// commit is believed, the rest read git at merge time, a skipped provider is
// left alone.
func TestAccount_CommitSourceIsNamed(t *testing.T) {
	records := []facts.ProviderRecord{
		{Name: "prism"},
		{Name: "self", Commit: "abc"},
		{Name: "absent", Skipped: true, Reason: "command not found"},
	}
	merged := []facts.Fact{{Kind: facts.KindSymbol, Name: "X", Props: map[string]any{PropProvider: "prism"}}}
	Account(records, merged, nil, &facts.GitInfo{Commit: "def", Dirty: true})
	if records[0].Commit != "def" || !records[0].Dirty || records[0].CommitSource != CommitSourceGit {
		t.Fatalf("git-stamped record: %+v", records[0])
	}
	if records[0].Overlap[""] != nil && records[0].Overlap == nil {
		t.Fatalf("overlap must be computed for a ran provider")
	}
	if records[1].Commit != "abc" || records[1].CommitSource != CommitSourceProvider {
		t.Fatalf("provider-stamped record: %+v", records[1])
	}
	if records[2].Commit != "" || records[2].CommitSource != "" || records[2].Overlap != nil {
		t.Fatalf("skipped record must stay untouched: %+v", records[2])
	}
}

// A provider's transitive ancestor is an addition the extractor never
// claimed; only a direct ancestor can contradict it.
func TestOverlap_TransitiveAncestorsAreNeverConflicts(t *testing.T) {
	extracted := []facts.Fact{{Kind: facts.KindSymbol, Name: "Order", Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "ApplicationRecord"}}}}
	provider := []facts.Fact{
		{Kind: facts.KindDependency, Name: "rubydex-ancestor: Order -> ActiveRecord::Base", Props: map[string]any{PropAncestorDistance: 2}, Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "ActiveRecord::Base"}}},
		{Kind: facts.KindDependency, Name: "rubydex-ancestor: Order -> Auditable", Props: map[string]any{PropAncestorDistance: 1}, Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "Auditable"}}},
	}
	got := Overlap(extracted, provider)[facts.RelImplements]
	if got.Conflict != 1 || got.ProviderOnly != 1 {
		t.Fatalf("implements: %+v", *got)
	}
}
