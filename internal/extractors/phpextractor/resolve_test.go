package phpextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// couplingEdges returns the set of "src -> dst" synthetic coupling edges in result.
func couplingEdges(result []facts.Fact) map[string]facts.Fact {
	m := make(map[string]facts.Fact)
	for _, f := range result {
		if f.Kind == facts.KindDependency && f.Props["synthetic_coupling"] == true {
			m[f.Name] = f
		}
	}
	return m
}

func TestResolveImports_InternalCoupling(t *testing.T) {
	// Two modules: app/service depends on app/models via a use-import, a static
	// call, and an instantiation. resolveImports should collapse them to a single
	// app/service -> app/models coupling edge.
	var all []facts.Fact
	all = append(all, extractFileAST([]byte(`<?php
namespace App\Models;
class Order { public static function find($id) {} }
`), "app/models/order.php")...)
	all = append(all, extractFileAST([]byte(`<?php
namespace App\Service;
use App\Models\Order;
class OrderService {
    public function run() {
        $o = new Order();
        Order::find(1);
    }
}
`), "app/service/order_service.php")...)
	// Module facts are needed by collectModuleNames-style consumers; add them as the
	// extractor would.
	all = append(all,
		facts.Fact{Kind: facts.KindModule, Name: "app/models", File: "app/models", Props: map[string]any{"language": "php"}},
		facts.Fact{Kind: facts.KindModule, Name: "app/service", File: "app/service", Props: map[string]any{"language": "php"}},
	)

	edges := couplingEdges(resolveImports(all, false))
	if _, ok := edges["app/service -> app/models"]; !ok {
		t.Errorf("expected coupling edge app/service -> app/models; got %v", keysDeps(edges))
	}

	// The use-import dependency should be classified internal.
	var sawInternal bool
	for _, f := range all {
		if f.Kind == facts.KindDependency && hasRel(f, facts.RelImports, "App\\Models\\Order") {
			if f.Props["source"] == "internal" {
				sawInternal = true
			}
		}
	}
	if !sawInternal {
		t.Errorf("use-import of App\\Models\\Order should be classified internal")
	}
}

func TestResolveImports_ExternalUnresolved(t *testing.T) {
	all := extractFileAST([]byte(`<?php
namespace App;
use GuzzleHttp\Client;
class Api { public function go() { $c = new Client(); } }
`), "app/api.php")
	all = append(all, facts.Fact{Kind: facts.KindModule, Name: "app", File: "app", Props: map[string]any{"language": "php"}})

	resolveImports(all, false)

	for _, f := range all {
		if f.Kind == facts.KindDependency && hasRel(f, facts.RelImports, "GuzzleHttp\\Client") {
			if f.Props["source"] != "external" {
				t.Errorf("unresolved vendor import should be external; got %v", f.Props["source"])
			}
			return
		}
	}
	t.Errorf("expected a use-import dependency for GuzzleHttp\\Client")
}

func keysDeps(m map[string]facts.Fact) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
