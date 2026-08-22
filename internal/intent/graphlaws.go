package intent

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

var routeMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true, "ANY": true}

var runtimeMetrics = map[string]bool{"queries": true}

var uniqueProps = map[string]bool{"table": true, "name": true}

const sinceLayout = "2006-01-02"

func graphComponentProblems(loc string, c ConstraintComponent) []string {
	var problems []string
	for _, method := range c.Handles {
		if !routeMethods[strings.ToUpper(method)] {
			problems = append(problems, fmt.Sprintf("%s (%s): handles %q is not an HTTP method (allowed: %s)", loc, c.Name, method, joinSortedKeys(routeMethods)))
		}
	}
	if len(c.Handles) > 0 && c.Kind != "" && c.Kind != "symbol" {
		problems = append(problems, fmt.Sprintf("%s (%s): handles selects the code behind routes, so kind must be symbol or absent, not %q", loc, c.Name, c.Kind))
	}
	if c.GovernedBy != "" {
		page, _ := SplitGovernedBy(c.GovernedBy)
		if page == "" || !validPageGlob(page) {
			problems = append(problems, fmt.Sprintf("%s (%s): governed_by %q must name a page path or bounded glob, optionally followed by status:<status> or supersedes:<page>", loc, c.Name, c.GovernedBy))
		}
	}
	return problems
}

// SplitGovernedBy separates the page selector from its optional qualifier:
// "wiki/x/adrs/*.md status:superseded" or "wiki/x/adrs/y.md supersedes:z.md".
func SplitGovernedBy(value string) (page, qualifier string) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "", ""
	}
	return fields[0], strings.Join(fields[1:], " ")
}

func graphRuleProblems(loc string, r ConstraintRule) []string {
	var problems []string
	switch {
	case r.CapRuntime != "":
		if !runtimeMetrics[r.Metric] {
			problems = append(problems, fmt.Sprintf("%s (%s): cap_runtime needs a metric the runtime provider measures (allowed: %s)", loc, r.ID, joinSortedKeys(runtimeMetrics)))
		}
		if r.Max <= 0 {
			problems = append(problems, fmt.Sprintf("%s (%s): cap_runtime needs a positive max", loc, r.ID))
		}
	case r.UniqueAcross != "":
		if !uniqueProps[r.By] {
			problems = append(problems, fmt.Sprintf("%s (%s): unique_across needs by naming the shared property (allowed: %s)", loc, r.ID, joinSortedKeys(uniqueProps)))
		}
	}
	if r.Metric != "" && r.CapRuntime == "" {
		problems = append(problems, fmt.Sprintf("%s (%s): metric belongs to cap_runtime", loc, r.ID))
	}
	if r.By != "" && r.UniqueAcross == "" {
		problems = append(problems, fmt.Sprintf("%s (%s): by belongs to unique_across", loc, r.ID))
	}
	if r.Growth != 0 && r.Cap == "" {
		problems = append(problems, fmt.Sprintf("%s (%s): growth belongs to cap", loc, r.ID))
	}
	if r.Growth < 0 {
		problems = append(problems, fmt.Sprintf("%s (%s): growth must not be negative", loc, r.ID))
	}
	if r.Since != "" {
		if _, err := time.Parse(sinceLayout, r.Since); err != nil {
			problems = append(problems, fmt.Sprintf("%s (%s): since %q must be a calendar date as YYYY-MM-DD", loc, r.ID, r.Since))
		}
	}
	return problems
}

func graphComponentProps(c ConstraintComponent, extra map[string]any) {
	if len(c.Handles) > 0 {
		methods := make([]string, 0, len(c.Handles))
		for _, m := range c.Handles {
			methods = append(methods, strings.ToUpper(m))
		}
		sort.Strings(methods)
		extra["handles"] = strings.Join(methods, " ")
	}
	if c.GovernedBy != "" {
		extra["governed_by"] = c.GovernedBy
	}
}

func graphFormProps(r ConstraintRule, extra map[string]any) {
	switch {
	case r.StorageStaysHome != "":
		extra["storage_stays_home"] = r.StorageStaysHome
	case r.CapRuntime != "":
		extra["cap_runtime"] = r.CapRuntime
		extra["metric"] = r.Metric
		extra["max"] = r.Max
	case r.RequireConsumer != "":
		extra["require_consumer"] = r.RequireConsumer
	case r.UniqueAcross != "":
		extra["unique_across"] = r.UniqueAcross
		extra["by"] = r.By
	case r.RequireGoverned != "":
		extra["require_governed"] = r.RequireGoverned
	}
	if r.Since != "" {
		extra["since"] = r.Since
	}
	if r.Growth > 0 {
		extra["growth"] = r.Growth
	}
}

func joinSortedKeys(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// validPageGlob admits a page path, a directory followed by /**, or a
// directory followed by one basename pattern such as *.md.
func validPageGlob(pattern string) bool {
	if pattern == "" || strings.Contains(pattern, "***") {
		return false
	}
	if strings.HasSuffix(pattern, "/**") {
		return !strings.ContainsAny(strings.TrimSuffix(pattern, "/**"), "*?[]{}")
	}
	dir, base := path.Split(pattern)
	if strings.ContainsAny(dir, "*?[]{}") || base == "" {
		return false
	}
	return strings.Count(base, "*") <= 1 && !strings.ContainsAny(base, "?[]{}")
}
