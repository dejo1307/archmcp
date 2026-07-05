package tsextractor

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// gRPC-web client detection.
//
// A gRPC call from TypeScript is, on the wire, an HTTP POST to
// "/pkg.Service/Method". We emit each *call site* as a client-role KindRoute
// with that Name so it matches the server RPC routes the grpc extractor emits
// from the .proto — feeding the same cross-repo linker and unused-routes
// explainer HTTP uses. Only methods actually called are emitted, so an RPC the
// frontend never invokes correctly surfaces as unused.
//
// Resolution is two-phase because a client's service definition and its call
// sites usually live in different files:
//  1. buildGRPCStubIndex scans generated stub files repo-wide for the service's
//     fully-qualified name, its RPC methods, and the client class name.
//  2. extractGRPCClientFacts (per file) binds `new XxxClient(...)` variables to
//     their service and emits a route for every `variable.method(...)` call
//     whose method is a known RPC.

// grpcService is one resolved gRPC service: its fully-qualified proto name and a
// map from the TypeScript method name (lowerCamel) to the proto method name.
type grpcService struct {
	fq      string
	methods map[string]string // tsMethod -> ProtoMethod
}

// grpcStubIndex is the repo-wide map of generated gRPC client stubs, keyed by
// the client class name (e.g. "UserServiceClient").
type grpcStubIndex struct {
	byClass map[string]*grpcService
}

func (idx *grpcStubIndex) empty() bool { return idx == nil || len(idx.byClass) == 0 }

var (
	// @protobuf-ts / connect-es doc comment: "@generated from protobuf service users.v1.UserService"
	reGenService = regexp.MustCompile(`@generated from protobuf service ([\w.]+)`)
	// @protobuf-ts / connect-es doc comment: "@generated from protobuf rpc: GetUser("
	reGenRPC = regexp.MustCompile(`@generated from protobuf rpc:\s*(\w+)\s*\(`)
	// @protobuf-ts runtime: new ServiceType("users.v1.UserService", [ { name: "GetUser", ... }, ... ])
	reServiceType = regexp.MustCompile(`new ServiceType\(\s*"([\w.]+)"\s*,\s*\[([\s\S]*?)\]\s*\)`)
	reMethodName  = regexp.MustCompile(`name:\s*"(\w+)"`)
	// connect-es service metadata: typeName: "users.v1.UserService"
	reTypeName = regexp.MustCompile(`typeName:\s*"([\w.]+)"`)
	// exported generated client class: "export class UserServiceClient"
	reClientClass = regexp.MustCompile(`export class (\w+)`)

	// A binding of a variable to a gRPC client construction:
	//   const userService = new UserServiceClient(transport)
	//   private readonly client: UserServiceClient = new UserServiceClient(t)
	reNewClientBinding = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*(?::\s*\w+)?\s*=\s*new\s+(\w+)\s*\(`)
	// A typed field/param bound to a client class (constructor injection):
	//   constructor(private users: UserServiceClient)
	reTypedClientBinding = regexp.MustCompile(`(\w+)\s*:\s*(\w+Client)\b`)
	// A method call on a receiver: userService.createUser(
	reReceiverCall = regexp.MustCompile(`(\w+)\.(\w+)\s*\(`)
	// An inline call on a freshly-constructed client: new UserServiceClient(t).createUser(
	reInlineCall = regexp.MustCompile(`new\s+(\w+)\s*\([^;]*?\)\s*\.\s*(\w+)\s*\(`)
)

// looksLikeGRPCStub reports whether a file's bytes carry any generated-gRPC
// marker, used to gate the (otherwise wasteful) stub pre-scan to real stubs.
func looksLikeGRPCStub(src []byte) bool {
	return bytes.Contains(src, []byte("@generated from protobuf")) ||
		bytes.Contains(src, []byte("new ServiceType(")) ||
		bytes.Contains(src, []byte("@protobuf-ts")) ||
		(bytes.Contains(src, []byte("typeName:")) && bytes.Contains(src, []byte("MethodKind")))
}

// buildGRPCStubIndex scans the TS files for generated gRPC client stubs and
// returns the class→service index used to resolve call sites. It reads files
// itself (a cheap marker check skips non-stub files) because the index must be
// complete before the per-file extraction pass runs.
func buildGRPCStubIndex(repoPath string, tsFiles []string) *grpcStubIndex {
	idx := &grpcStubIndex{byClass: map[string]*grpcService{}}

	for _, rel := range tsFiles {
		src, err := os.ReadFile(filepath.Join(repoPath, rel))
		if err != nil {
			continue
		}
		if !looksLikeGRPCStub(src) {
			continue
		}
		text := string(src)

		fq := serviceFQN(text)
		if fq == "" {
			continue
		}
		methods := protoMethods(text)
		if len(methods) == 0 {
			continue
		}
		svc := &grpcService{fq: fq, methods: map[string]string{}}
		for _, m := range methods {
			svc.methods[lowerFirst(m)] = m
		}

		// Associate every client class declared in this stub file, plus the
		// conventional "<ServiceName>Client" name derived from the FQN.
		for _, m := range reClientClass.FindAllStringSubmatch(text, -1) {
			if cls := m[1]; strings.HasSuffix(cls, "Client") {
				idx.byClass[cls] = svc
			}
		}
		if conv := lastSegment(fq) + "Client"; idx.byClass[conv] == nil {
			idx.byClass[conv] = svc
		}
	}

	if len(idx.byClass) == 0 {
		return nil
	}
	return idx
}

// serviceFQN pulls the fully-qualified service name from a stub file, trying the
// @generated doc comment, the @protobuf-ts ServiceType literal, then a connect
// typeName field.
func serviceFQN(text string) string {
	if m := reGenService.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	if m := reServiceType.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	if m := reTypeName.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// protoMethods returns the proto method names declared in a stub file, from the
// @generated rpc comments or the ServiceType method list.
func protoMethods(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, m := range reGenRPC.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	if len(out) == 0 {
		if m := reServiceType.FindStringSubmatch(text); m != nil {
			for _, mm := range reMethodName.FindAllStringSubmatch(m[2], -1) {
				add(mm[1])
			}
		}
	}
	return out
}

// extractGRPCClientFacts emits a client-role route per gRPC call site in one
// file, resolving receivers against the repo-wide stub index.
func extractGRPCClientFacts(src []byte, relFile string, idx *grpcStubIndex) []facts.Fact {
	if idx.empty() {
		return nil
	}
	dir := filepath.ToSlash(filepath.Dir(relFile))
	text := string(src)

	// varName -> *grpcService for locally bound clients.
	bound := map[string]*grpcService{}
	for _, m := range reNewClientBinding.FindAllStringSubmatch(text, -1) {
		if svc := idx.byClass[m[2]]; svc != nil {
			bound[m[1]] = svc
		}
	}
	for _, m := range reTypedClientBinding.FindAllStringSubmatch(text, -1) {
		if svc := idx.byClass[m[2]]; svc != nil {
			bound[m[1]] = svc
		}
	}

	var out []facts.Fact
	seen := map[string]bool{}
	emit := func(svc *grpcService, tsMethod string, off int) {
		proto, ok := svc.methods[tsMethod]
		if !ok {
			return
		}
		path := "/" + svc.fq + "/" + proto
		line := 1 + bytes.Count(src[:off], []byte("\n"))
		key := path + "\x00" + strconv.Itoa(line)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, facts.Fact{
			Kind: facts.KindRoute,
			Name: path,
			File: relFile,
			Line: line,
			Props: map[string]any{
				"role":        "client",
				"method":      "POST",
				"framework":   "grpc",
				"language":    "typescript",
				"source":      "ts-grpc-client",
				"type":        "grpc",
				"rpc_service": svc.fq,
				"rpc_method":  proto,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}

	for _, m := range reReceiverCall.FindAllSubmatchIndex(src, -1) {
		recv := string(src[m[2]:m[3]])
		method := string(src[m[4]:m[5]])
		if svc := bound[recv]; svc != nil {
			emit(svc, method, m[0])
		}
	}
	for _, m := range reInlineCall.FindAllSubmatchIndex(src, -1) {
		cls := string(src[m[2]:m[3]])
		method := string(src[m[4]:m[5]])
		if svc := idx.byClass[cls]; svc != nil {
			emit(svc, method, m[0])
		}
	}
	return out
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func lastSegment(fq string) string {
	if i := strings.LastIndex(fq, "."); i >= 0 {
		return fq[i+1:]
	}
	return fq
}
