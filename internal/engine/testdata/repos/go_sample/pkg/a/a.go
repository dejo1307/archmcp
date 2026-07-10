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
