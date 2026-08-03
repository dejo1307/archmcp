package facts

import "strings"

// Small read/copy helpers on Fact. They exist because the same four operations were
// being redeclared as unexported functions in every package that post-processes an
// assembled store — reading a string prop through a possibly-nil map, testing for an
// existing relation, copying props before a rewrite. Four copies of a nil check is
// four chances to forget one.
//
// They are value-receiver methods so they work on both a Fact and a *Fact without the
// caller thinking about it: every one is a read, and CloneProps returns a new map
// rather than touching the receiver.

// PropString reads a string prop, returning "" when the map is nil, the key is absent,
// or the value is not a string. The three cases collapse deliberately: every caller
// treats them the same, and distinguishing them has never been the question being
// asked.
func (f Fact) PropString(key string) string {
	if f.Props == nil {
		return ""
	}
	v, _ := f.Props[key].(string)
	return v
}

// PropBool reads a bool prop, returning false when absent or not a bool. It tolerates
// the JSON round-trip a restored snapshot goes through, where a bool survives as a
// bool but nothing guarantees the key exists at all.
func (f Fact) PropBool(key string) bool {
	if f.Props == nil {
		return false
	}
	v, _ := f.Props[key].(bool)
	return v
}

// HasRelation reports whether the fact already carries this exact edge. It is what
// makes a binder idempotent across appends: every binder re-runs on every snapshot,
// and without this check each run would append a duplicate relation.
func (f Fact) HasRelation(kind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == kind && r.Target == target {
			return true
		}
	}
	return false
}

// CloneProps returns a shallow copy of the props map, so a rewritten fact does not
// mutate the map the original shares. Returns a non-nil empty map for a nil source,
// so the caller can assign into the result unconditionally.
func (f Fact) CloneProps() map[string]any {
	out := make(map[string]any, len(f.Props)+1)
	for k, v := range f.Props {
		out[k] = v
	}
	return out
}

// ShortName returns the substring after the final '.', or the whole string when there
// is none. It turns a proto FQN ("users.v1.UserService") into the service short name
// ("UserService"), and a registration-site handler expression
// ("h.analyticsHandler.GetRoles") into the method name ("GetRoles") — the two places a
// generated-code convention leaves the interesting identifier in the last segment.
func ShortName(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
