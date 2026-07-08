package pythonextractor

import (
	"bytes"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/enola-labs/enola/internal/facts"
)

// Python gRPC client detection.
//
// A gRPC call from Python is, on the wire, an HTTP POST to "/pkg.Service/Method".
// We emit each call site as a client-role KindRoute so it matches the server RPC
// routes the grpc extractor emits from the .proto — feeding the same cross-repo
// linker and unused-routes explainer HTTP uses.
//
// Unlike the TypeScript/Go client detectors, we do NOT pre-scan generated stub
// files: real Python repos (vosk-server, airflow) build *_pb2_grpc.py at build
// time and never commit them, so there is nothing to scan. Instead we detect the
// hand-written source patterns directly:
//
//	stub = stt_service_pb2_grpc.SttServiceStub(channel)   # bind var -> "SttService"
//	stub.StreamingRecognize(gen(path))                    # call -> /SttService/StreamingRecognize
//
// The Python source only knows the SHORT service name ("SttService"); the
// fully-qualified wire path ("vosk.stt.v1.SttService") lives in the .proto. So we
// emit a PROVISIONAL short Name plus a grpc_service_short prop, and the engine's
// resolvePyGRPCClientRoutes pass (which sees the proto's server routes) rewrites
// the Name to the fully-qualified path before cross-repo linking.
//
// Binding is POSITIONAL, not last-wins: the same variable is routinely rebound to
// a second stub (vosk reuses `stub` for SttServiceStub then StatsServiceStub), so
// we interleave bindings and calls by source offset and resolve each call against
// the most recent preceding binding of its receiver.

var (
	// A stub construction bound to a variable:
	//   stub = stt_service_pb2_grpc.SttServiceStub(channel)
	//   s = SttServiceStub(channel)            # `from x_pb2_grpc import SttServiceStub`
	// The optional `[\w.]*?` prefix skips the (possibly namespaced) module receiver;
	// group 2 is the client class name minus its "Stub" suffix (the short service).
	reGRPCStubBinding = regexp.MustCompile(`(\w+)\s*=\s*[\w.]*?(\w+)Stub\s*\(`)
	// A method call on a receiver: stub.StreamingRecognize(
	reGRPCReceiverCall = regexp.MustCompile(`(\w+)\.(\w+)\s*\(`)
)

// looksLikePyGRPCClient gates the (otherwise wasteful) scan to files that import a
// generated gRPC stub module, so unrelated "*Stub" classes never produce routes.
func looksLikePyGRPCClient(src []byte) bool {
	return bytes.Contains(src, []byte("_pb2_grpc"))
}

// grpcEvent is a binding or a call, ordered by byte offset so calls resolve against
// the most recent preceding binding of their receiver.
type grpcEvent struct {
	off    int
	isBind bool
	a      string // bind: varName;  call: receiver var
	b      string // bind: short service (class minus "Stub");  call: method
}

// extractPyGRPCClientFacts emits a client-role route per gRPC stub call site in one
// file. Names are provisional (short service); the engine resolves them to the
// fully-qualified wire path.
func extractPyGRPCClientFacts(src []byte, relFile string) []facts.Fact {
	if !looksLikePyGRPCClient(src) {
		return nil
	}

	var events []grpcEvent
	for _, m := range reGRPCStubBinding.FindAllSubmatchIndex(src, -1) {
		events = append(events, grpcEvent{
			off:    m[0],
			isBind: true,
			a:      string(src[m[2]:m[3]]),
			b:      string(src[m[4]:m[5]]),
		})
	}
	for _, m := range reGRPCReceiverCall.FindAllSubmatchIndex(src, -1) {
		events = append(events, grpcEvent{
			off: m[0],
			a:   string(src[m[2]:m[3]]),
			b:   string(src[m[4]:m[5]]),
		})
	}
	if len(events) == 0 {
		return nil
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].off < events[j].off })

	dir := filepath.ToSlash(filepath.Dir(relFile))
	current := map[string]string{} // receiver var -> short service
	seen := map[string]bool{}
	var out []facts.Fact

	for _, ev := range events {
		if ev.isBind {
			current[ev.a] = ev.b
			continue
		}
		short := current[ev.a]
		if short == "" {
			continue
		}
		path := "/" + short + "/" + ev.b
		line := 1 + bytes.Count(src[:ev.off], []byte("\n"))
		key := path + "\x00" + strconv.Itoa(line)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, facts.Fact{
			Kind: facts.KindRoute,
			Name: path,
			File: relFile,
			Line: line,
			Props: map[string]any{
				"role":               "client",
				"method":             "POST",
				"framework":          "grpc",
				"language":           "python",
				"source":             "python-grpc-client",
				"type":               "grpc",
				"rpc_method":         ev.b,
				"grpc_service_short": short,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}
	return out
}
