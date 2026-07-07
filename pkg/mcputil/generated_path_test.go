package mcputil

import "testing"

func TestIsGeneratedPath(t *testing.T) {
	generated := []string{
		// Pre-existing segment matches.
		"src/build/x.go", "app/node_modules/pkg/i.js", "gen/generated/y.ts",
		// New segment matches (vendored, codegen dirs).
		"vendor/github.com/x/y.go", "ui/openapi-gen/requests/core.ts",
		"proj/third_party/lib.cc", "app/__generated__/schema.ts",
		// New suffix matches (codegen / minified files not under a marker dir).
		"airflow/ui/openapi-gen/requests/client/utils.gen.ts",
		"a/b/params.gen.tsx", "svc/api.pb.go", "svc/api_pb2.py",
		"svc/api_pb2_grpc.py", "web/app.min.js", "web/app.min.css",
	}
	for _, p := range generated {
		if !IsGeneratedPath(p) {
			t.Errorf("IsGeneratedPath(%q) = false, want true", p)
		}
	}

	source := []string{
		"airflow/models/dagrun.py", "internal/perf/perf.go",
		"src/components/Graph/reactflowUtils.ts",
		// "generator" is not the exact segment "generated"; ".genesis.ts" is not ".gen.ts".
		"pkg/generator/x.go", "src/genesis.ts", "app/vendored_helpers.py",
	}
	for _, p := range source {
		if IsGeneratedPath(p) {
			t.Errorf("IsGeneratedPath(%q) = true, want false", p)
		}
	}
}
