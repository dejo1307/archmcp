package tsextractor

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/enola-labs/enola/internal/factpath"
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

// grpcStubIndex is the repo-wide map of generated gRPC client stubs. byClass is
// keyed by the client class name (e.g. "UserServiceClient", @protobuf-ts /
// grpc-web); byService is keyed by the exported service-definition const name
// (e.g. "UserService", connect-es) that a consumer passes to createClient(...).
//
// ambiguousClass and ambiguousService record the names that must never resolve.
// Both maps are keyed by a SHORT name, so a service declared in two proto
// packages — ordinary API-versioning practice — collides here. Keeping the last
// writer would answer confidently from a derivation that failed, and would
// answer differently depending on the order the files were scanned in. The sets
// are sticky: once a name is dropped, neither a later stub file nor a
// conventional name derived from a fully-qualified one puts it back.
//
// Re-registration under the SAME fully-qualified name is not a collision. A
// service legitimately registers from more than one file — connect-es splits
// _pb and _connect, a barrel re-exports, a checked-in dist/ copy repeats the
// whole stub — and treating those as ambiguous would cost real edges.
type grpcStubIndex struct {
	byClass          map[string]*grpcService
	byService        map[string]*grpcService
	ambiguousClass   map[string]bool
	ambiguousService map[string]bool
}

func (idx *grpcStubIndex) empty() bool {
	return idx == nil || (len(idx.byClass) == 0 && len(idx.byService) == 0)
}

// bindClass registers a client class name against its service, dropping the name
// the moment a service with a differing fully-qualified name claims it.
func (idx *grpcStubIndex) bindClass(name string, svc *grpcService) {
	if name == "" || idx.ambiguousClass[name] {
		return
	}
	if existing, present := idx.byClass[name]; present && existing.fq != svc.fq {
		idx.ambiguousClass[name] = true
		delete(idx.byClass, name)
		return
	}
	idx.byClass[name] = svc
}

// bindService registers a service-definition const name, under the same rule.
func (idx *grpcStubIndex) bindService(name string, svc *grpcService) {
	if name == "" || idx.ambiguousService[name] {
		return
	}
	if existing, present := idx.byService[name]; present && existing.fq != svc.fq {
		idx.ambiguousService[name] = true
		delete(idx.byService, name)
		return
	}
	idx.byService[name] = svc
}

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
	//   const c = new proto.users.v1.UserServiceClient(host)  // commonjs grpc-web
	// The optional `[\w.]+\.` prefix captures the last segment of a namespaced
	// constructor.
	reNewClientBinding = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*(?::\s*\w+)?\s*=\s*new\s+(?:[\w.]+\.)?(\w+)\s*\(`)
	// A typed field/param bound to a client class (constructor injection):
	//   constructor(private users: UserServiceClient)
	reTypedClientBinding = regexp.MustCompile(`(\w+)\s*:\s*(\w+Client)\b`)
	// A method call on a receiver: userService.createUser(
	reReceiverCall = regexp.MustCompile(`(\w+)\.(\w+)\s*\(`)
	// An inline call on a freshly-constructed client: new UserServiceClient(t).createUser(
	reInlineCall = regexp.MustCompile(`new\s+(\w+)\s*\([^;]*?\)\s*\.\s*(\w+)\s*\(`)
	// connect-es service definition const: export const UserService = ...
	reServiceConst = regexp.MustCompile(`export const (\w+)\b`)
	// connect-es client binding: const c = createClient(UserService, transport)
	// (also createPromiseClient, the connect-es v1 name), optionally generic.
	reConnectClientBinding = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*=\s*create(?:Promise)?Client\s*(?:<[^>]*>)?\s*\(\s*(\w+)`)
	// A gRPC full-method path as a quoted string literal, as it appears in classic
	// grpc-web generated clients (MethodDescriptor / rpcCall). The service segment
	// requires a dot so it never matches an incidental REST path like "/a/b".
	reProcedureLiteral = regexp.MustCompile(`['"]/([A-Za-z_][\w.]*\.[A-Za-z_]\w*)/([A-Za-z_]\w*)['"]`)
)

// looksLikeGRPCStub reports whether a file's bytes carry any generated-gRPC
// marker, used to gate the (otherwise wasteful) stub pre-scan to real stubs.
func looksLikeGRPCStub(src []byte) bool {
	return bytes.Contains(src, []byte("@generated from protobuf")) ||
		bytes.Contains(src, []byte("new ServiceType(")) ||
		bytes.Contains(src, []byte("@protobuf-ts")) ||
		(bytes.Contains(src, []byte("typeName:")) && bytes.Contains(src, []byte("MethodKind"))) ||
		// classic grpc-web generated client (protoc-gen-grpc-web)
		bytes.Contains(src, []byte("MethodDescriptor(")) ||
		bytes.Contains(src, []byte(".rpcCall(")) ||
		bytes.Contains(src, []byte("_grpc_web_pb")) ||
		bytes.Contains(src, []byte("grpc-web"))
}

// buildGRPCStubIndex scans the TS files for generated gRPC client stubs and
// returns the class→service index used to resolve call sites. It reads files
// itself (a cheap marker check skips non-stub files) because the index must be
// complete before the per-file extraction pass runs.
func buildGRPCStubIndex(repoPath string, tsFiles []string) *grpcStubIndex {
	idx := &grpcStubIndex{
		byClass:          map[string]*grpcService{},
		byService:        map[string]*grpcService{},
		ambiguousClass:   map[string]bool{},
		ambiguousService: map[string]bool{},
	}

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
				idx.bindClass(cls, svc)
			}
		}
		idx.bindClass(lastSegment(fq)+"Client", svc)

		// connect-es: a consumer passes the exported service-definition const to
		// createClient(...), so associate each `export const X` in this stub file
		// with its service. Also register the conventional "<ServiceName>" name.
		for _, m := range reServiceConst.FindAllStringSubmatch(text, -1) {
			idx.bindService(m[1], svc)
		}
		idx.bindService(lastSegment(fq), svc)
	}

	if idx.empty() {
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
	// grpc-web: no metadata markers — derive the service from a method-path literal.
	if m := reProcedureLiteral.FindStringSubmatch(text); m != nil {
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
	// connect-es: the service definition is a plain object literal
	// (`methods: { getUser: { name: "GetUser", ... } }`) rather than a
	// `new ServiceType(...)` — fall back to any `name: "X"` in the (already
	// gated) stub file.
	if len(out) == 0 {
		for _, mm := range reMethodName.FindAllStringSubmatch(text, -1) {
			add(mm[1])
		}
	}
	// grpc-web: derive method names from the method-path literals.
	if len(out) == 0 {
		for _, mm := range reProcedureLiteral.FindAllStringSubmatch(text, -1) {
			add(mm[2])
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
	dir := factpath.Dir(relFile)
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
	// connect-es: const c = createClient(UserService, transport)
	for _, m := range reConnectClientBinding.FindAllStringSubmatch(text, -1) {
		if svc := idx.byService[m[2]]; svc != nil {
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
				facts.PropRole:      facts.RoleClient,
				"method":            "POST",
				facts.PropFramework: facts.FrameworkGRPC,
				"language":          "typescript",
				facts.PropSource:    facts.RouteSourceTSGRPCClient,
				"type":              "grpc",
				"rpc_service":       svc.fq,
				"rpc_method":        proto,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: dir}},
		})
	}

	for _, m := range reReceiverCall.FindAllSubmatchIndex(src, -1) {
		// The string() conversions are inlined into the map lookups so the
		// compiler elides the []byte→string allocation (staticcheck SA6001).
		if svc := bound[string(src[m[2]:m[3]])]; svc != nil {
			emit(svc, string(src[m[4]:m[5]]), m[0])
		}
	}
	for _, m := range reInlineCall.FindAllSubmatchIndex(src, -1) {
		if svc := idx.byClass[string(src[m[2]:m[3]])]; svc != nil {
			emit(svc, string(src[m[4]:m[5]]), m[0])
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
