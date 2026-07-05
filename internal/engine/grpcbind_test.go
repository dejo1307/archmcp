package engine

import (
	"testing"

	"github.com/enola-labs/enola/internal/config"
	"github.com/enola-labs/enola/internal/facts"
)

// grpcServerRoute builds a server-role gRPC route fact like the grpc extractor emits.
func grpcServerRoute(service, method string) facts.Fact {
	return facts.Fact{
		Kind: facts.KindRoute,
		Name: "/" + service + "/" + method,
		Props: map[string]any{
			"type": "grpc", "role": "server", "framework": "grpc",
			"rpc_service": service, "rpc_method": method,
		},
	}
}

func routeHandledBy(f facts.Fact) (string, bool) {
	for _, r := range f.Relations {
		if r.Kind == facts.RelHandledBy {
			return r.Target, true
		}
	}
	return "", false
}

func TestBindGRPCHandlers_BindsViaEmbedConvention(t *testing.T) {
	eng, _ := New(config.Default())
	eng.Store().Add(
		grpcServerRoute("users.v1.UserService", "GetUser"),
		grpcServerRoute("users.v1.UserService", "CreateUser"),
		facts.Fact{
			Kind: facts.KindSymbol, Name: "users.UserService",
			Props: map[string]any{"symbol_kind": facts.SymbolStruct},
			Relations: []facts.Relation{
				{Kind: facts.RelImplements, Target: "usersv1.UnimplementedUserServiceServer"},
			},
		},
		facts.Fact{Kind: facts.KindSymbol, Name: "users.UserService.GetUser", Props: map[string]any{"symbol_kind": facts.SymbolMethod}},
		facts.Fact{Kind: facts.KindSymbol, Name: "users.UserService.CreateUser", Props: map[string]any{"symbol_kind": facts.SymbolMethod}},
	)

	eng.bindGRPCHandlers()

	for _, method := range []string{"GetUser", "CreateUser"} {
		routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/users.v1.UserService/" + method})
		if len(routes) != 1 {
			t.Fatalf("route lookup for %s = %d, want 1", method, len(routes))
		}
		target, ok := routeHandledBy(routes[0])
		if !ok {
			t.Fatalf("%s route has no handled_by relation", method)
		}
		want := "users.UserService." + method
		if target != want {
			t.Errorf("handled_by target = %q, want %q", target, want)
		}
		if routes[0].Props["handler"] != want {
			t.Errorf("handler prop = %v, want %q", routes[0].Props["handler"], want)
		}
	}
}

func TestBindGRPCHandlers_NoEmbedNoBind(t *testing.T) {
	eng, _ := New(config.Default())
	eng.Store().Add(
		grpcServerRoute("users.v1.UserService", "GetUser"),
		// Struct implements some unrelated interface, not Unimplemented*Server.
		facts.Fact{
			Kind: facts.KindSymbol, Name: "users.UserService",
			Props:     map[string]any{"symbol_kind": facts.SymbolStruct},
			Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "io.Closer"}},
		},
		facts.Fact{Kind: facts.KindSymbol, Name: "users.UserService.GetUser", Props: map[string]any{"symbol_kind": facts.SymbolMethod}},
	)

	eng.bindGRPCHandlers()

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/users.v1.UserService/GetUser"})
	if _, ok := routeHandledBy(routes[0]); ok {
		t.Error("route was bound despite no Unimplemented*Server embed")
	}
}

func TestBindGRPCHandlers_MissingMethodNotBound(t *testing.T) {
	eng, _ := New(config.Default())
	eng.Store().Add(
		grpcServerRoute("users.v1.UserService", "GetUser"),
		facts.Fact{
			Kind: facts.KindSymbol, Name: "users.UserService",
			Props:     map[string]any{"symbol_kind": facts.SymbolStruct},
			Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "usersv1.UnimplementedUserServiceServer"}},
		},
		// No users.UserService.GetUser method symbol present.
	)

	eng.bindGRPCHandlers()

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/users.v1.UserService/GetUser"})
	if _, ok := routeHandledBy(routes[0]); ok {
		t.Error("route bound to a nonexistent method")
	}
}

func TestBindGRPCHandlers_AmbiguousShortNameSkipped(t *testing.T) {
	eng, _ := New(config.Default())
	eng.Store().Add(
		grpcServerRoute("users.v1.UserService", "GetUser"),
		// Two distinct structs both embed UnimplementedUserServiceServer.
		facts.Fact{
			Kind: facts.KindSymbol, Name: "a.UserService", Repo: "",
			Props:     map[string]any{"symbol_kind": facts.SymbolStruct},
			Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "usersv1.UnimplementedUserServiceServer"}},
		},
		facts.Fact{
			Kind: facts.KindSymbol, Name: "b.UserService",
			Props:     map[string]any{"symbol_kind": facts.SymbolStruct},
			Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "usersv1.UnimplementedUserServiceServer"}},
		},
		facts.Fact{Kind: facts.KindSymbol, Name: "a.UserService.GetUser", Props: map[string]any{"symbol_kind": facts.SymbolMethod}},
		facts.Fact{Kind: facts.KindSymbol, Name: "b.UserService.GetUser", Props: map[string]any{"symbol_kind": facts.SymbolMethod}},
	)

	eng.bindGRPCHandlers()

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/users.v1.UserService/GetUser"})
	if _, ok := routeHandledBy(routes[0]); ok {
		t.Error("ambiguous service short name should not bind")
	}
}

func TestBindGRPCHandlers_Idempotent(t *testing.T) {
	eng, _ := New(config.Default())
	eng.Store().Add(
		grpcServerRoute("users.v1.UserService", "GetUser"),
		facts.Fact{
			Kind: facts.KindSymbol, Name: "users.UserService",
			Props:     map[string]any{"symbol_kind": facts.SymbolStruct},
			Relations: []facts.Relation{{Kind: facts.RelImplements, Target: "usersv1.UnimplementedUserServiceServer"}},
		},
		facts.Fact{Kind: facts.KindSymbol, Name: "users.UserService.GetUser", Props: map[string]any{"symbol_kind": facts.SymbolMethod}},
	)

	eng.bindGRPCHandlers()
	eng.bindGRPCHandlers() // second run must not duplicate the relation

	routes, _ := eng.Store().QueryAdvanced(facts.QueryOpts{Kind: facts.KindRoute, Name: "/users.v1.UserService/GetUser"})
	n := 0
	for _, r := range routes[0].Relations {
		if r.Kind == facts.RelHandledBy {
			n++
		}
	}
	if n != 1 {
		t.Errorf("handled_by relations = %d, want 1 (idempotent)", n)
	}
}
