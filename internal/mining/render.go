package mining

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/enola-labs/enola/internal/intent"
)

func renderDeclarationYAML(components []intent.ConstraintComponent, rule intent.ConstraintRule) string {
	var b strings.Builder
	b.WriteString("components:\n")
	for _, c := range components {
		fmt.Fprintf(&b, "  - name: %s\n", yamlQuote(c.Name))
		if c.Service != "" {
			fmt.Fprintf(&b, "    service: %s\n", yamlQuote(c.Service))
		}
		if c.Kind != "" {
			fmt.Fprintf(&b, "    kind: %s\n", yamlQuote(c.Kind))
		}
		b.WriteString("    match:\n")
		for _, m := range c.Match {
			fmt.Fprintf(&b, "      - %s\n", yamlQuote(m))
		}
	}
	b.WriteString("rules:\n")
	fmt.Fprintf(&b, "  - id: %s\n", yamlQuote(rule.ID))
	switch {
	case rule.Require != "":
		fmt.Fprintf(&b, "    require: %s\n", yamlQuote(rule.Require))
		if rule.WhenPropContains != nil {
			b.WriteString("    when_prop_contains:\n")
			fmt.Fprintf(&b, "      prop: %s\n", yamlQuote(rule.WhenPropContains.Prop))
			fmt.Fprintf(&b, "      value: %s\n", yamlQuote(rule.WhenPropContains.Value))
		}
		b.WriteString("    must_prop_contain:\n")
		fmt.Fprintf(&b, "      prop: %s\n", yamlQuote(rule.MustPropContain.Prop))
		fmt.Fprintf(&b, "      value: %s\n", yamlQuote(rule.MustPropContain.Value))
	case rule.RequireName != "":
		fmt.Fprintf(&b, "    require_name: %s\n", yamlQuote(rule.RequireName))
		fmt.Fprintf(&b, "    pattern: %s\n", yamlQuote(rule.Pattern))
	case rule.RequireDefines != "":
		fmt.Fprintf(&b, "    require_defines: %s\n", yamlQuote(rule.RequireDefines))
		fmt.Fprintf(&b, "    method: %s\n", yamlQuote(rule.Method))
	case rule.Forbid != "":
		fmt.Fprintf(&b, "    forbid: %s\n", yamlQuote(rule.Forbid))
		fmt.Fprintf(&b, "    to: %s\n", yamlQuote(rule.To))
		fmt.Fprintf(&b, "    via: %s\n", yamlQuote(rule.Via))
	case rule.Allow != "":
		fmt.Fprintf(&b, "    allow: %s\n", yamlQuote(rule.Allow))
		b.WriteString("    only:\n")
		for _, o := range rule.Only {
			fmt.Fprintf(&b, "      - %s\n", yamlQuote(o))
		}
		fmt.Fprintf(&b, "    via: %s\n", yamlQuote(rule.Via))
	}
	fmt.Fprintf(&b, "    mode: %s\n", yamlQuote(rule.Mode))
	fmt.Fprintf(&b, "    because: %s\n", yamlQuote(rule.Because))
	return b.String()
}

func yamlQuote(s string) string {
	return strconv.Quote(s)
}

func (r *Report) WriteText(w io.Writer, top int) {
	var b strings.Builder
	fmt.Fprintf(&b, "Mined %d candidate constraints over %d facts — proposals for operator review, never self-adopting law.\n", len(r.Candidates), r.FactCount)
	fmt.Fprintf(&b, "Thresholds: support >= %d, confidence >= %.2f, exceptions <= %d per candidate. Ranked by confidence x support.\n",
		r.Config.MinSupport, r.Config.MinConfidence, r.Config.MaxExceptions)

	shown := r.Candidates
	if top > 0 && len(shown) > top {
		shown = shown[:top]
	}
	for i, c := range shown {
		fmt.Fprintf(&b, "\n%3d. %.3f x %-6d [%s", i+1, c.Confidence, c.Denominator, c.Family)
		if c.Kind != "" {
			fmt.Fprintf(&b, " %s", c.Kind)
		}
		fmt.Fprintf(&b, "] %s\n", c.Statement)
		if len(c.Exceptions) == 0 {
			fmt.Fprintf(&b, "     exceptions: none in the mined snapshot\n")
		} else {
			names := make([]string, len(c.Exceptions))
			for j, e := range c.Exceptions {
				if e.File != "" {
					names[j] = e.Name + " (" + e.File + ")"
				} else {
					names[j] = e.Name
				}
			}
			fmt.Fprintf(&b, "     exceptions (%d): %s\n", len(c.Exceptions), strings.Join(names, ", "))
		}
		fmt.Fprintf(&b, "     would-be rule (adopt by hand: copy into enola/constraints/, rewrite because:, review mode):\n")
		for _, line := range strings.Split(strings.TrimRight(c.YAML, "\n"), "\n") {
			fmt.Fprintf(&b, "       %s\n", line)
		}
	}
	if len(r.Candidates) > len(shown) {
		fmt.Fprintf(&b, "\n... and %d more candidates below rank %d — raise --top or read the --jsonl artifact.\n", len(r.Candidates)-len(shown), len(shown))
	}

	fmt.Fprintf(&b, "\nSuppressed below the floors (counted, never silent):\n")
	tautological := 0
	for _, sc := range r.Suppressed {
		fmt.Fprintf(&b, "  %-17s %d would-be candidates under the support floor of %d; %d over the exception ceiling of %d; %d tautological\n",
			sc.Family+":", sc.BelowSupportFloor, r.Config.MinSupport, sc.OverExceptionCeiling, r.Config.MaxExceptions, sc.Tautological)
		tautological += sc.Tautological
	}
	if tautological > 0 {
		fmt.Fprintf(&b, "%d tautological candidate(s) suppressed — the statement holds by construction; --include-tautologies prints them\n", tautological)
	}
	_, _ = io.WriteString(w, b.String())
}

func (r *Report) WriteJSONL(w io.Writer) error {
	enc := json.NewEncoder(w)
	header := map[string]any{
		"type":           "mining-report",
		"min_support":    r.Config.MinSupport,
		"min_confidence": r.Config.MinConfidence,
		"max_exceptions": r.Config.MaxExceptions,
		"fact_count":     r.FactCount,
		"candidates":     len(r.Candidates),
	}
	if err := enc.Encode(header); err != nil {
		return err
	}
	for i, c := range r.Candidates {
		line := struct {
			Type string `json:"type"`
			Rank int    `json:"rank"`
			Candidate
		}{Type: "candidate", Rank: i + 1, Candidate: c}
		if err := enc.Encode(line); err != nil {
			return err
		}
	}
	for _, sc := range r.Suppressed {
		line := struct {
			Type string `json:"type"`
			SuppressedCount
		}{Type: "suppressed", SuppressedCount: sc}
		if err := enc.Encode(line); err != nil {
			return err
		}
	}
	return nil
}
