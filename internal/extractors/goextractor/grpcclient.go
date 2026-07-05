package goextractor

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"unicode"

	"github.com/enola-labs/enola/internal/facts"
)

// Go gRPC client detection.
//
// A Go consumer of a gRPC service writes:
//
//	client := usersv1.NewUserServiceClient(conn)
//	resp, _ := client.GetUser(ctx, req)
//
// The call site carries no wire path; the authoritative "/pkg.Service/Method"
// lives only as a string literal inside the *generated* concrete client method
// (`c.cc.Invoke(ctx, "/users.v1.UserService/GetUser", …)` for unary, or
// `.NewStream(ctx, …, "/pkg.Service/Method", …)` for streaming). So detection is
// two-phase, mirroring the TypeScript detector (tsextractor/grpcclient.go):
//   1. buildGoGRPCStubIndex scans the generated concrete clients repo-wide for
//      (client interface, method) → path.
//   2. extractGRPCClientFacts binds `NewXxxClient(...)` variables per file and
//      emits a client-role route for each call to a known method.

// goGRPCStub is one resolved gRPC client: its methods mapped to wire paths.
type goGRPCStub struct {
	methods map[string]string // Go method name → "/fq.Service/Method"
}

// goGRPCStubIndex maps an exported client interface name (e.g. "UserServiceClient",
// the type a consumer's NewUserServiceClient(...) binding yields) to its methods.
type goGRPCStubIndex struct {
	byClient map[string]*goGRPCStub
}

func (idx *goGRPCStubIndex) empty() bool { return idx == nil || len(idx.byClient) == 0 }

// buildGoGRPCStubIndex scans every parsed Go file for generated concrete gRPC
// client methods and returns the interface→method→path index. It reads only the
// already-parsed ASTs (no file re-reads). Returns nil when nothing is found.
func buildGoGRPCStubIndex(files []*ast.File) *goGRPCStubIndex {
	idx := &goGRPCStubIndex{byClient: map[string]*goGRPCStub{}}

	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 || fn.Body == nil {
				continue
			}
			recv := receiverTypeName(fn.Recv.List[0].Type)
			// protoc-gen-go-grpc names the concrete client "<service>Client"
			// (unexported); the exported interface a consumer binds to is its
			// upper-cased form.
			if !strings.HasSuffix(recv, "Client") || isExportedName(recv) {
				continue
			}
			path := invokePathInBody(fn.Body)
			if path == "" {
				continue
			}
			iface := upperFirst(recv)
			stub := idx.byClient[iface]
			if stub == nil {
				stub = &goGRPCStub{methods: map[string]string{}}
				idx.byClient[iface] = stub
			}
			stub.methods[fn.Name.Name] = path
		}
	}

	if len(idx.byClient) == 0 {
		return nil
	}
	return idx
}

// invokePathInBody returns the first "/…"-prefixed string literal passed to a
// `.Invoke(...)` or `.NewStream(...)` call in a function body — the gRPC full
// method path — or "" if none is present.
func invokePathInBody(body *ast.BlockStmt) string {
	var path string
	ast.Inspect(body, func(n ast.Node) bool {
		if path != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Invoke" && sel.Sel.Name != "NewStream") {
			return true
		}
		for _, arg := range call.Args {
			if s := stringLit(arg); strings.HasPrefix(s, "/") {
				path = s
				return false
			}
		}
		return true
	})
	return path
}

// extractGRPCClientFacts emits a client-role route for each gRPC client call site
// in a file, resolving receivers against the repo-wide stub index.
func extractGRPCClientFacts(fset *token.FileSet, f *ast.File, relFile, pkgDir string, idx *goGRPCStubIndex) []facts.Fact {
	if idx.empty() {
		return nil
	}

	// Bind local variables assigned from a NewXxxClient(...) constructor to the
	// client interface name (file-wide, like the TS detector's `bound` map).
	bound := map[string]string{} // var name → client interface (e.g. "UserServiceClient")
	ast.Inspect(f, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			iface := clientCtorInterface(rhs)
			if iface == "" || i >= len(assign.Lhs) {
				continue
			}
			if id, ok := assign.Lhs[i].(*ast.Ident); ok && idx.byClient[iface] != nil {
				bound[id.Name] = iface
			}
		}
		return true
	})

	var out []facts.Fact
	seen := map[string]bool{}
	emit := func(iface, method string, pos token.Pos) {
		stub := idx.byClient[iface]
		if stub == nil {
			return
		}
		path := stub.methods[method]
		if path == "" {
			return
		}
		line := fset.Position(pos).Line
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
				"language":    "go",
				"source":      "go-grpc-client",
				"type":        "grpc",
				"rpc_service": grpcServiceOf(path),
				"rpc_method":  grpcMethodOf(path),
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: pkgDir}},
		})
	}

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// recv.Method(...) on a bound client variable.
		if recv, ok := sel.X.(*ast.Ident); ok {
			if iface := bound[recv.Name]; iface != "" {
				emit(iface, sel.Sel.Name, call.Pos())
			}
			return true
		}
		// Inline: pkg.NewXxxClient(conn).Method(...) or NewXxxClient(conn).Method(...).
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			if iface := clientCtorInterface(inner); iface != "" {
				emit(iface, sel.Sel.Name, call.Pos())
			}
		}
		return true
	})
	return out
}

// clientCtorInterface returns the client interface name a `NewXxxClient(...)`
// constructor call yields (e.g. "UserServiceClient"), or "" if expr is not such
// a call. Handles both `NewXxxClient(...)` and `pkg.NewXxxClient(...)`.
func clientCtorInterface(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	var name string
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		name = fn.Sel.Name
	default:
		return ""
	}
	rest := strings.TrimPrefix(name, "New")
	if rest == name || !strings.HasSuffix(rest, "Client") || rest == "Client" {
		return ""
	}
	return rest
}

// receiverTypeName returns the bare type name of a method receiver, unwrapping a
// pointer receiver (`*userServiceClient` → "userServiceClient").
func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func stringLit(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}

// grpcServiceOf returns "users.v1.UserService" from "/users.v1.UserService/GetUser".
func grpcServiceOf(path string) string {
	p := strings.TrimPrefix(path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return p
}

// grpcMethodOf returns "GetUser" from "/users.v1.UserService/GetUser".
func grpcMethodOf(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func isExportedName(s string) bool {
	if s == "" {
		return false
	}
	return unicode.IsUpper([]rune(s)[0])
}
