package status

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageKeyDeterministicAndDistinct(t *testing.T) {
	a1 := usageKey("/src/enola")
	a2 := usageKey("/src/enola")
	b := usageKey("/src/vendored/enola")

	if a1 != a2 {
		t.Errorf("usageKey not stable for same path: %q vs %q", a1, a2)
	}
	if a1 == b {
		t.Error("usageKey should differ for different paths")
	}
	// Human-readable base is preserved.
	if !strings.HasPrefix(a1, "enola-") {
		t.Errorf("usageKey should start with the repo base name: %q", a1)
	}
	// Two repos sharing a base name must not collide (hash disambiguates).
	x := usageKey("/a/proj")
	y := usageKey("/b/proj")
	if x == y {
		t.Error("same base name in different dirs must produce different keys")
	}
}

func TestUsagePathUnderUsageDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := usageDir()
	if err != nil {
		t.Fatal(err)
	}
	p, err := usagePath("/some/repo")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != dir {
		t.Errorf("usagePath dir = %q, want %q", filepath.Dir(p), dir)
	}
	if filepath.Ext(p) != ".json" {
		t.Errorf("usagePath should end in .json: %q", p)
	}
}

func TestSanitizeBase(t *testing.T) {
	if got := sanitizeBase("my repo/weird:name"); strings.ContainsAny(got, " /:") {
		t.Errorf("sanitizeBase left unsafe chars: %q", got)
	}
	if got := sanitizeBase("ok-name_1.2"); got != "ok-name_1.2" {
		t.Errorf("sanitizeBase mangled safe chars: %q", got)
	}
}
