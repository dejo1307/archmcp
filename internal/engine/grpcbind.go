package engine

import (
	"log"
	"regexp"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// unimplementedEmbed matches the target of the `implements` edge a Go gRPC
// server impl carries by embedding protoc-gen-go-grpc's forward-compat base
// type — e.g. "usersv1.UnimplementedUserServiceServer" (optionally package- or
// alias-qualified) — capturing the service short name ("UserService").
var unimplementedEmbed = regexp.MustCompile(`^(?:.*\.)?Unimplemented(.+)Server$`)

// servicerBase matches the target of the `implements` edge a Python gRPC servicer
// carries by subclassing its generated base — e.g.
// "stt_service_pb2_grpc.SttServiceServicer" — capturing the service short name
// ("SttService"). grpc_python_out always names the base "<Service>Servicer".
var servicerBase = regexp.MustCompile(`^(?:.*\.)?(.+)Servicer$`)

// bindGRPCHandlers connects each gRPC server route (emitted from a .proto by the
// grpc extractor) to the Go method that implements it, so route → handler is
// traversable by impact_analysis and find_path.
//
// The bridge is the protoc-gen-go-grpc forward-compatibility convention: a
// server impl embeds Unimplemented<Service>Server, which the Go extractor
// already records as an `implements` edge on the impl struct. The embedded
// type's short name ("UserService") equals the last segment of the route's
// rpc_service ("users.v1.UserService"), so the route's rpc_method maps to the
// struct's method symbol ("users.UserService.<Method>").
//
// It runs post-extraction (like flagUnmatchedRoutes) over the assembled store,
// before BuildGraph, and is idempotent, so it recomputes safely on every
// snapshot and append without any per-extractor cache involvement.
func (e *Engine) bindGRPCHandlers() {
	symbols := e.store.ByKind(facts.KindSymbol)

	// Per-repo index of service short name → impl struct symbol name, plus the
	// set of method symbol names (for existence checks). Both are scoped by repo
	// so a route only ever binds to a handler in its own repo.
	implMap := map[string]map[string]string{} // repo → shortName → struct name
	ambiguous := map[string]map[string]bool{} // repo → shortName → seen twice
	methodSet := map[string]map[string]bool{} // repo → method name → exists

	for _, s := range symbols {
		kind, _ := s.Props["symbol_kind"].(string)
		switch kind {
		case facts.SymbolStruct, facts.SymbolClass:
			short := implShortName(s)
			if short == "" {
				continue
			}
			if implMap[s.Repo] == nil {
				implMap[s.Repo] = map[string]string{}
				ambiguous[s.Repo] = map[string]bool{}
			}
			if existing, ok := implMap[s.Repo][short]; ok && existing != s.Name {
				ambiguous[s.Repo][short] = true
			} else {
				implMap[s.Repo][short] = s.Name
			}
		case facts.SymbolMethod:
			if methodSet[s.Repo] == nil {
				methodSet[s.Repo] = map[string]bool{}
			}
			methodSet[s.Repo][s.Name] = true
		}
	}

	bound := 0
	e.store.UpdateWhere(func(f *facts.Fact) {
		if f.Kind != facts.KindRoute || f.Props == nil {
			return
		}
		if f.Props[facts.PropRouteType] != facts.RouteTypeGRPC || f.Props[facts.PropRole] != facts.RoleServer {
			return
		}
		short := lastDotSegment(propStr(f, "rpc_service"))
		method := propStr(f, "rpc_method")
		if short == "" || method == "" {
			return
		}
		if ambiguous[f.Repo][short] {
			return // two impls claim this service short name — don't guess
		}
		impl := implMap[f.Repo][short]
		if impl == "" {
			return
		}
		target := impl + "." + method
		if !methodSet[f.Repo][target] {
			return
		}
		if hasRelation(f, facts.RelHandledBy, target) {
			return // idempotent across appends
		}
		f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelHandledBy, Target: target})
		f.Props["handler"] = target
		bound++
	})
	if bound > 0 {
		log.Printf("[engine] bound %d gRPC server route(s) to their Go handler", bound)
	}
}

// resolvePyGRPCClientRoutes rewrites provisional Python gRPC client routes to their
// fully-qualified wire path. The Python extractor only sees the short service name
// (e.g. "SttService"), so it emits Name = "/SttService/StreamingRecognize" plus a
// grpc_service_short prop; the fully-qualified name ("vosk.stt.v1.SttService") lives
// only in the .proto. This pass resolves short → fq from the gRPC server routes in
// the store and rewrites the Name to "/vosk.stt.v1.SttService/StreamingRecognize"
// so it matches the server route at cross-repo link time.
//
// It MUST run before linkCrossRepo (which matches routes by Name). It re-resolves
// from the preserved grpc_service_short prop each run, so it is idempotent across
// appends. A client route whose service has no proto in the snapshot (or an
// ambiguous short name) is left provisional.
func (e *Engine) resolvePyGRPCClientRoutes() {
	// Proto index from server routes: short service name → fq, plus per-fq method
	// sets and an ambiguity guard (two packages sharing a short service name).
	fqOf := map[string]string{}
	ambiguous := map[string]bool{}
	methodsOf := map[string]map[string]bool{}
	for _, r := range e.store.ByKind(facts.KindRoute) {
		if valProp(r, facts.PropRouteType) != facts.RouteTypeGRPC || valProp(r, facts.PropRole) != facts.RoleServer {
			continue
		}
		fq := valProp(r, "rpc_service")
		if fq == "" {
			continue
		}
		short := lastDotSegment(fq)
		if prev, ok := fqOf[short]; ok && prev != fq {
			ambiguous[short] = true
		} else {
			fqOf[short] = fq
		}
		if methodsOf[fq] == nil {
			methodsOf[fq] = map[string]bool{}
		}
		if m := valProp(r, "rpc_method"); m != "" {
			methodsOf[fq][m] = true
		}
	}

	// Collect + resolve client routes, then remove-and-re-add so the store's name
	// index stays consistent (in-place Name mutation would desync byName).
	var replaced []facts.Fact
	found := false
	for _, r := range e.store.ByKind(facts.KindRoute) {
		if valProp(r, facts.PropSource) != facts.RouteSourcePythonGRPCClient {
			continue
		}
		found = true
		short := valProp(r, "grpc_service_short")
		method := valProp(r, "rpc_method")
		fq := fqOf[short]
		if short != "" && method != "" && fq != "" && !ambiguous[short] && methodsOf[fq][method] {
			r.Props = cloneProps(r.Props)
			r.Name = "/" + fq + "/" + method
			r.Props["rpc_service"] = fq
		}
		replaced = append(replaced, r)
	}
	if !found {
		return
	}
	e.store.RemoveWhere(func(f facts.Fact) bool {
		return f.Kind == facts.KindRoute && valProp(f, facts.PropSource) == facts.RouteSourcePythonGRPCClient
	})
	e.store.Add(replaced...)
}

// valProp reads a string prop from a value Fact, tolerating a nil Props map.
func valProp(f facts.Fact, key string) string {
	if f.Props == nil {
		return ""
	}
	v, _ := f.Props[key].(string)
	return v
}

// cloneProps returns a shallow copy of a props map so a rewritten fact does not
// mutate the shared original.
func cloneProps(p map[string]any) map[string]any {
	out := make(map[string]any, len(p)+1)
	for k, v := range p {
		out[k] = v
	}
	return out
}

// implShortName returns the gRPC service short name a symbol implements — a Go
// struct embedding Unimplemented<Service>Server, or a Python class subclassing a
// generated <Service>Servicer base — or "" if it implements no such type.
func implShortName(s facts.Fact) string {
	for _, r := range s.Relations {
		if r.Kind != facts.RelImplements {
			continue
		}
		if m := unimplementedEmbed.FindStringSubmatch(r.Target); m != nil {
			return m[1]
		}
		if m := servicerBase.FindStringSubmatch(r.Target); m != nil {
			return m[1]
		}
	}
	return ""
}

func hasRelation(f *facts.Fact, kind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == kind && r.Target == target {
			return true
		}
	}
	return false
}

func propStr(f *facts.Fact, key string) string {
	if f.Props == nil {
		return ""
	}
	v, _ := f.Props[key].(string)
	return v
}

// lastDotSegment returns the substring after the final '.', or the whole string
// if there is none — turning a proto FQN "users.v1.UserService" into the service
// short name "UserService".
func lastDotSegment(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
