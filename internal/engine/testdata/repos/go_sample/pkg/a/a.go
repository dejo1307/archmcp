package a

import "example.com/gosample/pkg/b"

// Alpha calls into package b, forming one side of a deliberate a<->b import
// cycle so the cycles explainer (Tarjan SCC) has something to detect.
func Alpha() {
	b.Beta()
}

// GetPath walks a parent chain, one lookup per level. The bare `for {}` adds no factor
// of n to Big-O, but it repeats — so getByID must stay in calls_in_scaling_loop, the N+1
// candidate set. Modelled on golf's OrganizationRepository.GetOrganizationPath, whose
// high-severity finding an earlier draft of v99 deleted. (v99)
func GetPath(id int) {
	for {
		getByID(id)
	}
}

// Seed's only in-loop call sits inside a constant loop, so calls_in_scaling_loop is
// emitted EMPTY rather than omitted — an omitted key would make the consumer fall back
// to the unfiltered calls_in_loop. (v99)
func Seed() {
	for _, c := range []string{"a", "b"} {
		setup(c)
	}
}

func getByID(id int) {}
func setup(c string) {}

// helper has no production caller. Its only reference is from a_test.go, which is
// ignored for indexing and recovered as a test_ref fact — so the graph must still
// carry an incoming edge for it. Before v100 no such fact existed for Go and the
// symbol looked dead. Pins GAP-GO-06 for the in-package test idiom. (v100)
func helper(n int) int { return n * 2 }

// Gamma is the same case for the OTHER Go test idiom: its only reference is from
// a_ext_test.go (`package a_test`), which reaches it through an import alias rather
// than unqualified. Deliberately not Alpha — Alpha's dependent count is asserted by
// TestE2E_ImpactAnalysis, and a test_ref counts as a dependent (see GAP-XL-15). (v100)
func Gamma() {}
