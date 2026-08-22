package constraints

import "fmt"

// One vocabulary for both ends of an edge. A verdict about an edge is a claim
// about two resolutions — how the source was reached, and how the target was —
// and before this there were three boolean helpers wording one of them each,
// one of which was named for membership while its argument described the
// target. A boolean cannot say what a source side has to: a fact can be a
// member, or a fact the declaration says a member owns, or joined only through
// a file. Three states, said the same way at both ends, so a reader learns one
// concept rather than two.
//
// Every basis here is measured. The distinction the sentence has to carry is
// not certain-versus-guessed but how far the join reached: an exact resolution
// names the fact itself, an owned one names a member's METHOD which the
// DECLARATION says is the member's, and a grounded one names no fact at all and
// joins through the measured file. A verdict calling any of the weaker two
// exact would claim a precision it did not have, which is the failure this
// vocabulary exists to make unwriteable.
type basis int

const (
	// exactBasis: the fact IS a member of the component.
	exactBasis basis = iota
	// ownedBasis: the fact is not a member; it is a member's method and the
	// component's declaration says a member's methods are the member's.
	ownedBasis
	// groundedBasis: the fact resolves through a measured FILE rather than by
	// name — a path-shaped edge target, or a dependency carrier sharing the
	// component's files.
	groundedBasis
)

// sourcePhrase words how the fact that MADE the edge was reached.
func (b basis) sourcePhrase() string {
	switch b {
	case ownedBasis:
		return "the source is a method of a member, which the declaration says is the member's"
	case groundedBasis:
		return "the source names no member and rides a dependency fact of a file the component's patterns name"
	default:
		return "the source membership is exact"
	}
}

// targetPhrase words how the fact the edge LANDED ON was reached.
func (b basis) targetPhrase() string {
	switch b {
	case ownedBasis:
		return "the target is a method of a member, which the declaration says is the member's"
	case groundedBasis:
		return "the target names no measured fact and grounds on the measured file it names"
	default:
		return "the target membership is exact"
	}
}

// edgeBasis words both ends of one measured edge in one clause — the sentence
// every edge form's verdict carries, so no form can state one end and leave the
// other to be assumed exact.
func edgeBasis(source, target basis) string {
	if source == exactBasis && target == exactBasis {
		return "both memberships are exact"
	}
	return fmt.Sprintf("%s and %s", source.sourcePhrase(), target.targetPhrase())
}

// disallowedBasis words an allow-only verdict, whose target was resolved as a
// measured thing and then found inside no allowed component. Its landing clause
// is therefore about the target itself rather than about a membership: there is
// no component it belongs to, which is the breach.
func disallowedBasis(source, target basis) string {
	landing := "the target names a measured fact"
	if target == groundedBasis {
		landing = "the target names no measured fact and grounds on the measured file it names"
	}
	return source.sourcePhrase() + ", and " + landing
}

// reverseBasis words a verdict from a form that walks the whole graph looking
// for arrivals — protect and private. Only the target end resolved onto a
// component; the source end's whole claim is that it resolved onto none of the
// ones the rule allows, and the sentence has to say that rather than leave the
// source to be read as exactly measured.
func reverseBasis(target basis, role string) string {
	return fmt.Sprintf("%s and the source resolves onto no %s component", target.targetPhrase(), role)
}

// privateSubject names what a private verdict's target IS, which the three
// bases answer differently. An exact target names the non-exported member
// itself. An owned one names a method of that member, and the declaration is
// what makes reaching it a reach into the member. A grounded one is a path,
// and what the rule measured about it is a property of the FILE — that every
// member of the private component measured there is non-exported — so calling
// that path a member would name something no fact carries.
func privateSubject(b basis) string {
	switch b {
	case ownedBasis:
		return "is a method of a non-exported member of"
	case groundedBasis:
		return "is a measured file of"
	default:
		return "is a non-exported member of"
	}
}

// privateBasisPhrase words how that target was reached. The grounded arm states
// the file property rather than a membership, because that is what was measured.
func privateBasisPhrase(b basis) string {
	if b == groundedBasis {
		return "the target names no member and grounds on that file, whose every measured fact is non-exported"
	}
	return b.targetPhrase()
}
