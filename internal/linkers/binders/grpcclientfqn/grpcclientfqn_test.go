package grpcclientfqn

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func bind(t *testing.T, store *facts.Store) {
	t.Helper()
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatalf("Bind: %v", err)
	}
}

// grpcServerRoute builds a server-role gRPC route fact like the grpc extractor emits.
func grpcServerRoute(service, method string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindRoute,
		Name: "/" + service + "/" + method,
		Props: map[string]any{
			facts.PropRouteType: facts.RouteTypeGRPC,
			facts.PropRole:      facts.RoleServer,
			facts.PropFramework: facts.FrameworkGRPC,
			"rpc_service":       service,
			"rpc_method":        method,
		},
	}
}

// pyGRPCClientRoute builds a provisional (short-name) Python gRPC client route like
// the Python extractor emits before this binder resolves it to the FQ wire path.
func pyGRPCClientRoute(short, method string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindRoute,
		Name: "/" + short + "/" + method,
		Props: map[string]any{
			facts.PropRole:       facts.RoleClient,
			facts.PropFramework:  facts.FrameworkGRPC,
			facts.PropSource:     facts.RouteSourcePythonGRPCClient,
			facts.PropRouteType:  facts.RouteTypeGRPC,
			"method":             "POST",
			"language":           "python",
			"rpc_method":         method,
			"grpc_service_short": short,
		},
	}
}

// TestResolvePyGRPCClientRoutes_RewritesToFQ: a provisional short client route is
// rewritten to the fully-qualified wire path (matching the proto server route) and
// the short route no longer exists.
func TestResolvePyGRPCClientRoutes_RewritesToFQ(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		grpcServerRoute("vosk.stt.v1.SttService", "StreamingRecognize"),
		pyGRPCClientRoute("SttService", "StreamingRecognize"),
	)

	bind(t, store)

	if got, _ := store.QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/SttService/StreamingRecognize"}); len(got) != 0 {
		t.Errorf("short client route still present: %d", len(got))
	}
	routes, _ := store.QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/vosk.stt.v1.SttService/StreamingRecognize"})
	foundClient := false
	for _, r := range routes {
		if r.Props[facts.PropSource] == facts.RouteSourcePythonGRPCClient {
			foundClient = true
			if r.Props["rpc_service"] != "vosk.stt.v1.SttService" {
				t.Errorf("rpc_service = %v, want vosk.stt.v1.SttService", r.Props["rpc_service"])
			}
		}
	}
	if !foundClient {
		t.Error("client route was not rewritten to the fully-qualified name")
	}
}

// TestResolvePyGRPCClientRoutes_UnresolvedLeftShort: with no proto in the snapshot,
// the client route stays provisional (short) rather than being dropped or corrupted.
func TestResolvePyGRPCClientRoutes_UnresolvedLeftShort(t *testing.T) {
	store := facts.NewStore()
	store.Add(pyGRPCClientRoute("SttService", "StreamingRecognize"))

	bind(t, store)

	routes, _ := store.QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/SttService/StreamingRecognize"})
	if len(routes) != 1 {
		t.Errorf("unresolved client route should be left short, got %d routes", len(routes))
	}
}

// TestResolvePyGRPCClientRoutes_Idempotent pins the property the Binder contract
// requires and the original had by construction: this binder REWRITES facts rather
// than adding relations, so a second run must re-derive the same names rather than
// re-prefixing an already-resolved route into "/vosk.stt.v1.vosk.stt.v1.…".
func TestResolvePyGRPCClientRoutes_Idempotent(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		grpcServerRoute("vosk.stt.v1.SttService", "StreamingRecognize"),
		pyGRPCClientRoute("SttService", "StreamingRecognize"),
	)

	bind(t, store)
	bind(t, store)

	routes, _ := store.QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/vosk.stt.v1.SttService/StreamingRecognize"})
	clients := 0
	for _, r := range routes {
		if r.Props[facts.PropSource] == facts.RouteSourcePythonGRPCClient {
			clients++
		}
	}
	if clients != 1 {
		t.Errorf("client routes after two runs = %d, want 1", clients)
	}
}
