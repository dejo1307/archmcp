package engine_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/bootstrap"
)

// TestCrossRepoPHPHTTP verifies that a PHP consumer's outbound HTTP-client calls
// link to a PHP provider's server routes across an append-mode (multi-repo)
// snapshot — exercising the full path from PHP route/client extraction through the
// cross-repo linker. Mirrors how the MCP server auto-appends a second repo.
func TestCrossRepoPHPHTTP(t *testing.T) {
	eng, _, err := bootstrap.NewEngine(bootstrap.Options{
		ConfigPath: filepath.Join(t.TempDir(), "no-such-config.yaml"), // defaults
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	base := filepath.Join("testdata", "repos", "php_multirepo")
	// Provider first (append=false), then consumer (append=true).
	if _, err := eng.GenerateSnapshot(context.Background(), filepath.Join(base, "provider"), false); err != nil {
		t.Fatalf("snapshot provider: %v", err)
	}
	if _, err := eng.GenerateSnapshot(context.Background(), filepath.Join(base, "consumer"), true); err != nil {
		t.Fatalf("snapshot consumer: %v", err)
	}

	all := eng.Store().All()

	// 1. A cross-repo dependency edge consumer -> provider, via http-client.
	dep := findDependency(t, all, "consumer -> provider")
	if got := dep.Props["type"]; got != "cross_repo" {
		t.Errorf("dependency type = %v, want cross_repo", got)
	}
	if !propSliceContains(dep.Props["via"], "http-client") {
		t.Errorf("dependency via = %v, want to contain http-client", dep.Props["via"])
	}
	if got := dep.Props["confidence"]; got != "verified" {
		t.Errorf("confidence = %v, want verified", got)
	}
	if !propSliceContains(dep.Props["endpoints"], "GET /billing/invoices") ||
		!propSliceContains(dep.Props["endpoints"], "POST /billing/invoices") {
		t.Errorf("endpoints = %v, want GET+POST /billing/invoices", dep.Props["endpoints"])
	}

	// 2. The consumer service node carries a depends_on relation to provider.
	consumer := findService(t, all, "consumer")
	if !hasRelation(consumer, facts.RelDependsOn, "provider") {
		t.Errorf("consumer service missing depends_on->provider; relations=%v", consumer.Relations)
	}

	// 3. The provider's uncalled GET /billing/invoices/{id} is flagged for the
	//    unused-routes explainer (served, but matched by no loaded client).
	if !hasUnmatchedServerRoute(all, "/billing/invoices/{id}") {
		t.Errorf("expected /billing/invoices/{id} to be flagged unmatched_by_clients")
	}
}

func findDependency(t *testing.T, all []facts.Fact, name string) facts.Fact {
	t.Helper()
	for _, f := range all {
		if f.Kind == facts.KindDependency && f.Name == name {
			return f
		}
	}
	t.Fatalf("no dependency fact %q", name)
	return facts.Fact{}
}

func findService(t *testing.T, all []facts.Fact, name string) facts.Fact {
	t.Helper()
	for _, f := range all {
		if f.Kind == facts.KindService && f.Name == name {
			return f
		}
	}
	t.Fatalf("no service fact %q", name)
	return facts.Fact{}
}

func hasRelation(f facts.Fact, kind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == kind && r.Target == target {
			return true
		}
	}
	return false
}

func hasUnmatchedServerRoute(all []facts.Fact, path string) bool {
	for _, f := range all {
		if f.Kind == facts.KindRoute && f.Name == path && f.Props["unmatched_by_clients"] == true {
			return true
		}
	}
	return false
}

// propSliceContains reports whether a fact prop that is a []string (or []any of
// strings) contains want.
func propSliceContains(v any, want string) bool {
	switch s := v.(type) {
	case []string:
		for _, e := range s {
			if e == want {
				return true
			}
		}
	case []any:
		for _, e := range s {
			if str, ok := e.(string); ok && str == want {
				return true
			}
		}
	}
	return false
}
