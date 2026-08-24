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

// Metric accessors read Insight.Metrics, which a restored snapshot delivers with
// every number as a float64 however it was written. Each one collapses "absent",
// "nil map" and "wrong type" into the zero value for the same reason PropString
// does: no caller has ever needed to tell them apart.

// MetricInt reads an integer metric, tolerating the float64 a JSON round-trip
// leaves behind. Both forms occur in one process: an explainer writes int, and
// the same insight read back from a baseline arrives as float64.
func (in Insight) MetricInt(key string) int {
	if in.Metrics == nil {
		return 0
	}
	switch v := in.Metrics[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// MetricFloat reads a fractional metric, tolerating an integer written for a whole
// number.
func (in Insight) MetricFloat(key string) float64 {
	if in.Metrics == nil {
		return 0
	}
	switch v := in.Metrics[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

// MetricStrings reads a list-valued metric. A round-tripped snapshot delivers it
// as []any of strings rather than as []string, so both are accepted; a non-string
// element is skipped rather than failing the whole read, because a partial list is
// still usable and a caller has no way to act on the difference.
func (in Insight) MetricStrings(key string) []string {
	if in.Metrics == nil {
		return nil
	}
	switch v := in.Metrics[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// MetricStringMap reads a map-valued metric, accepting the map[string]any a JSON
// round-trip produces alongside the map[string]string an explainer writes.
func (in Insight) MetricStringMap(key string) map[string]string {
	if in.Metrics == nil {
		return nil
	}
	switch v := in.Metrics[key].(type) {
	case map[string]string:
		return v
	case map[string]any:
		out := make(map[string]string, len(v))
		for k, e := range v {
			if s, ok := e.(string); ok {
				out[k] = s
			}
		}
		return out
	}
	return nil
}
