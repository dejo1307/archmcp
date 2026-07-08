package pythonextractor

import (
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// grpcClientRoutes returns the (sorted) Names of client-role gRPC routes emitted
// for src, plus a lookup of Name → fact for prop assertions.
func grpcClientRoutes(src string, relFile string) ([]string, map[string]facts.Fact) {
	ff := extractPyGRPCClientFacts([]byte(src), relFile)
	names := make([]string, 0, len(ff))
	byName := make(map[string]facts.Fact, len(ff))
	for _, f := range ff {
		names = append(names, f.Name)
		byName[f.Name] = f
	}
	sort.Strings(names)
	return names, byName
}

// TestPyGRPC_ClientStubCall_EmitsRoute: a stub.Method(...) call emits a
// client-role route with the provisional short Name and the expected props.
func TestPyGRPC_ClientStubCall_EmitsRoute(t *testing.T) {
	src := `
import grpc
import stt_service_pb2_grpc

def run():
    channel = grpc.insecure_channel('localhost:5001')
    stub = stt_service_pb2_grpc.SttServiceStub(channel)
    return stub.StreamingRecognize(gen())
`
	names, byName := grpcClientRoutes(src, "client/stt_client.py")
	if len(names) != 1 || names[0] != "/SttService/StreamingRecognize" {
		t.Fatalf("routes = %v, want [/SttService/StreamingRecognize]", names)
	}
	r := byName["/SttService/StreamingRecognize"]
	checks := map[string]string{
		"role":               "client",
		"method":             "POST",
		"framework":          "grpc",
		"language":           "python",
		"source":             "python-grpc-client",
		"type":               "grpc",
		"rpc_method":         "StreamingRecognize",
		"grpc_service_short": "SttService",
	}
	for k, want := range checks {
		if got, _ := r.Props[k].(string); got != want {
			t.Errorf("prop %q = %q, want %q", k, got, want)
		}
	}
}

// TestPyGRPC_StubRebinding_PositionalBinding: the vosk pattern reuses one `stub`
// var for two services. Positional binding must attribute each call to the most
// recent preceding binding, so BOTH services' calls are emitted correctly.
func TestPyGRPC_StubRebinding_PositionalBinding(t *testing.T) {
	src := `
import grpc
import stt_service_pb2
import stt_service_pb2_grpc

def run(path):
    channel = grpc.insecure_channel('localhost:5001')
    stub = stt_service_pb2_grpc.SttServiceStub(channel)
    it = stub.StreamingRecognize(gen(path))
    for r in it:
        pass
    stub = stt_service_pb2_grpc.StatsServiceStub(channel)
    print(stub.GetStats(request=None))
`
	names, _ := grpcClientRoutes(src, "client/stt_client.py")
	want := []string{"/StatsService/GetStats", "/SttService/StreamingRecognize"}
	if len(names) != len(want) {
		t.Fatalf("routes = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("routes = %v, want %v", names, want)
		}
	}
}

// TestPyGRPC_NoStubImport_NoRoute: a file that never imports a *_pb2_grpc module is
// not gRPC — even a `FooStub(...)` construction must not produce a route.
func TestPyGRPC_NoStubImport_NoRoute(t *testing.T) {
	src := `
class FooStub:
    def Bar(self): ...

def run():
    stub = FooStub()
    return stub.Bar()
`
	names, _ := grpcClientRoutes(src, "app/service.py")
	if len(names) != 0 {
		t.Errorf("expected no routes for non-gRPC file, got %v", names)
	}
}

// TestPyGRPC_DynamicStubClass_NoRoute: the airflow GrpcHook pattern passes the stub
// class in at runtime and dispatches via getattr, so nothing is statically
// resolvable — we must not fabricate routes even though the file imports a grpc stub.
func TestPyGRPC_DynamicStubClass_NoRoute(t *testing.T) {
	src := `
import grpc
import stt_service_pb2_grpc

def run(stub_class, call_func, data):
    channel = grpc.insecure_channel('localhost:5001')
    stub = stub_class(channel)
    rpc_func = getattr(stub, call_func)
    return rpc_func(**data)
`
	names, _ := grpcClientRoutes(src, "hooks/grpc.py")
	if len(names) != 0 {
		t.Errorf("expected no routes for dynamic stub dispatch, got %v", names)
	}
}
