package diff

import (
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// A provider that ran on one side only contributes its whole fact set as a
// delta — the extractor-set failure mode arriving through a tool that was
// missing (or version-mismatched) on one machine, with no config edit at all.
// That is why the comparison covers the providers that RAN, not the ones that
// were configured.
func TestCompareMeta_ProviderSetDiffers(t *testing.T) {
	ran := func(name string) facts.ProviderRecord {
		return facts.ProviderRecord{Name: name, Version: "1.0.0", FactCount: 3}
	}
	skipped := func(name string) facts.ProviderRecord {
		return facts.ProviderRecord{Name: name, Skipped: true, Reason: "command not found"}
	}
	tests := []struct {
		name          string
		base, cur     []facts.ProviderRecord
		wantWarn      bool
		wantSubstring string
	}{
		{
			name: "identical ran sets do not warn",
			base: []facts.ProviderRecord{ran("prism")},
			cur:  []facts.ProviderRecord{ran("prism")},
		},
		{
			name: "skipped on both sides does not warn",
			base: []facts.ProviderRecord{ran("prism"), skipped("sorbet")},
			cur:  []facts.ProviderRecord{ran("prism"), skipped("sorbet")},
		},
		{
			name:          "skipped on one side only warns as REMOVED",
			base:          []facts.ProviderRecord{ran("prism")},
			cur:           []facts.ProviderRecord{skipped("prism")},
			wantWarn:      true,
			wantSubstring: "appear as REMOVED",
		},
		{
			name:          "ran on the current side only warns as ADDED",
			base:          nil,
			cur:           []facts.ProviderRecord{ran("prism")},
			wantWarn:      true,
			wantSubstring: "appear as ADDED",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, cur := comparablePair()
			base.Providers, cur.Providers = tc.base, tc.cur
			c := compareMeta(base, cur)
			if got := hasKind(c, WarnProviderSet); got != tc.wantWarn {
				t.Fatalf("provider_set warned = %v, want %v (warnings: %v)", got, tc.wantWarn, c.Warnings)
			}
			if !tc.wantWarn {
				if !c.Comparable {
					t.Fatalf("comparable pair reported warnings: %v", c.Warnings)
				}
				return
			}
			if !strings.Contains(strings.Join(c.Warnings, "\n"), tc.wantSubstring) {
				t.Errorf("warnings %v must carry %q", c.Warnings, tc.wantSubstring)
			}
			// The gate must decline, not caveat: a provider is a fact source,
			// so the delta under a differing set describes tooling, not the change.
			if !c.InvalidatesDelta() {
				t.Error("a differing ran-provider set must invalidate the delta")
			}
		})
	}
}
