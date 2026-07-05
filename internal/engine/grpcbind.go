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
		case facts.SymbolStruct:
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
		if f.Props["type"] != "grpc" || f.Props["role"] != "server" {
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

// implShortName returns the gRPC service short name a struct implements by
// embedding Unimplemented<Service>Server, or "" if it embeds no such type.
func implShortName(s facts.Fact) string {
	for _, r := range s.Relations {
		if r.Kind != facts.RelImplements {
			continue
		}
		if m := unimplementedEmbed.FindStringSubmatch(r.Target); m != nil {
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
