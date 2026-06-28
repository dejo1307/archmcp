package phpextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// --- shared test helpers ---

// symbolsByName indexes the symbol facts in a result by name.
func symbolsByName(result []facts.Fact) map[string]facts.Fact {
	m := make(map[string]facts.Fact)
	for _, f := range result {
		if f.Kind == facts.KindSymbol {
			m[f.Name] = f
		}
	}
	return m
}

// hasRel returns true if the fact has a relation of relKind to target.
func hasRel(f facts.Fact, relKind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == relKind && r.Target == target {
			return true
		}
	}
	return false
}

// factsByKind returns all facts of the given kind.
func factsByKind(result []facts.Fact, kind string) []facts.Fact {
	var out []facts.Fact
	for _, f := range result {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

// keys returns the sorted-insertion keys of a symbol index (for fatal messages).
func keys(m map[string]facts.Fact) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- tests ---

func TestExtractFile_ClassAndMethod(t *testing.T) {
	src := `<?php
namespace App\Models;

class Order {
    public function total(): int {
        return 0;
    }
    private function secret() {}
}
`
	result := extractFileAST([]byte(src), "app/models/order.php")
	byName := symbolsByName(result)

	cls, ok := byName["App\\Models\\Order"]
	if !ok {
		t.Fatalf("missing class App\\Models\\Order; got %v", keys(byName))
	}
	if sk, _ := cls.Props["symbol_kind"].(string); sk != facts.SymbolClass {
		t.Errorf("Order symbol_kind = %q, want class", sk)
	}
	if cls.Props["exported"] != true {
		t.Errorf("Order exported = %v, want true", cls.Props["exported"])
	}
	if !hasRel(cls, facts.RelDeclares, "app/models") {
		t.Errorf("Order should declare module app/models; relations %v", cls.Relations)
	}

	total, ok := byName["App\\Models\\Order::total"]
	if !ok {
		t.Fatalf("missing method App\\Models\\Order::total; got %v", keys(byName))
	}
	if sk, _ := total.Props["symbol_kind"].(string); sk != facts.SymbolMethod {
		t.Errorf("total symbol_kind = %q, want method", sk)
	}
	if total.Props["exported"] != true {
		t.Errorf("public total exported = %v, want true", total.Props["exported"])
	}

	secret := byName["App\\Models\\Order::secret"]
	if secret.Props["exported"] != false {
		t.Errorf("private secret exported = %v, want false", secret.Props["exported"])
	}
	if secret.Props["visibility"] != "private" {
		t.Errorf("secret visibility = %v, want private", secret.Props["visibility"])
	}
}

func TestExtractFile_GlobalFunction(t *testing.T) {
	src := `<?php
function get_the_author($deprecated = '') {
    return wp_get_current_user();
}
`
	result := extractFileAST([]byte(src), "wp-includes/author-template.php")
	byName := symbolsByName(result)

	fn, ok := byName["get_the_author"]
	if !ok {
		t.Fatalf("missing function get_the_author; got %v", keys(byName))
	}
	if sk, _ := fn.Props["symbol_kind"].(string); sk != facts.SymbolFunc {
		t.Errorf("symbol_kind = %q, want function", sk)
	}
	if !hasRel(fn, facts.RelCalls, "wp_get_current_user") {
		t.Errorf("get_the_author should call wp_get_current_user; relations %v", fn.Relations)
	}
	if !hasRel(fn, facts.RelDeclares, "wp-includes") {
		t.Errorf("function should declare module wp-includes; relations %v", fn.Relations)
	}
}

func TestExtractFile_InterfaceTraitEnum(t *testing.T) {
	src := `<?php
interface Repository {
    public function find(int $id);
}

trait Timestamps {
    public function touch() {}
}

enum Status: string {
    case Active = "active";
    case Inactive = "inactive";
}
`
	result := extractFileAST([]byte(src), "src/types.php")
	byName := symbolsByName(result)

	if sk, _ := byName["Repository"].Props["symbol_kind"].(string); sk != facts.SymbolInterface {
		t.Errorf("Repository symbol_kind = %q, want interface", sk)
	}
	tr, ok := byName["Timestamps"]
	if !ok {
		t.Fatal("missing trait Timestamps")
	}
	if sk, _ := tr.Props["symbol_kind"].(string); sk != facts.SymbolClass {
		t.Errorf("Timestamps symbol_kind = %q, want class (trait flag)", sk)
	}
	if tr.Props["trait"] != true {
		t.Errorf("Timestamps should have trait=true; props %v", tr.Props)
	}
	if sk, _ := byName["Status"].Props["symbol_kind"].(string); sk != facts.SymbolEnum {
		t.Errorf("Status symbol_kind = %q, want enum", sk)
	}
	if _, ok := byName["Status::Active"]; !ok {
		t.Errorf("missing enum case Status::Active; got %v", keys(byName))
	}
}

func TestExtractFile_InheritanceAndImplements(t *testing.T) {
	src := `<?php
namespace App;

use App\Contracts\Repository;

abstract class BaseModel extends \WP_Object implements Repository {
    public function find(int $id) {}
}
`
	result := extractFileAST([]byte(src), "app/base_model.php")
	byName := symbolsByName(result)

	cls := byName["App\\BaseModel"]
	if cls.Props["abstract"] != true {
		t.Errorf("BaseModel should be abstract; props %v", cls.Props)
	}
	// extends \WP_Object -> leading backslash stripped to FQN.
	if !hasRel(cls, facts.RelImplements, "WP_Object") {
		t.Errorf("BaseModel should implement/extend WP_Object; relations %v", cls.Relations)
	}
	// implements Repository -> expanded via the `use` import to its FQN.
	if !hasRel(cls, facts.RelImplements, "App\\Contracts\\Repository") {
		t.Errorf("BaseModel should implement App\\Contracts\\Repository; relations %v", cls.Relations)
	}
}

func TestExtractFile_UseImportAndStaticCall(t *testing.T) {
	src := `<?php
namespace App\Service;

use App\Models\Order;

class OrderService {
    public function run() {
        $o = new Order();
        Order::find(1);
    }
}
`
	result := extractFileAST([]byte(src), "app/service/order_service.php")

	deps := factsByKind(result, facts.KindDependency)
	var importedOrder bool
	for _, d := range deps {
		if hasRel(d, facts.RelImports, "App\\Models\\Order") {
			importedOrder = true
		}
	}
	if !importedOrder {
		t.Errorf("expected a use-import dependency for App\\Models\\Order; deps %v", deps)
	}

	run := symbolsByName(result)["App\\Service\\OrderService::run"]
	if !hasRel(run, facts.RelInstantiates, "App\\Models\\Order") {
		t.Errorf("run should instantiate App\\Models\\Order; relations %v", run.Relations)
	}
	if !hasRel(run, facts.RelCalls, "App\\Models\\Order::find") {
		t.Errorf("run should call App\\Models\\Order::find; relations %v", run.Relations)
	}
}

func TestExtract_EndToEnd_DetectAndModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "composer.json", `{"name":"acme/app"}`)
	writeFile(t, dir, "src/Order.php", `<?php
namespace Acme;
class Order { public function total() { return 1; } }
`)

	e := New()
	ok, err := e.Detect(dir)
	if err != nil || !ok {
		t.Fatalf("Detect = %v, %v; want true, nil", ok, err)
	}

	result, err := e.Extract(context.Background(), dir, []string{"composer.json", "src/Order.php"})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	mods := factsByKind(result, facts.KindModule)
	var haveSrc bool
	for _, m := range mods {
		if m.Name == "src" {
			haveSrc = true
			if m.Props["language"] != "php" {
				t.Errorf("module language = %v, want php", m.Props["language"])
			}
		}
	}
	if !haveSrc {
		t.Errorf("expected a module fact for src; modules %v", mods)
	}
	if _, ok := symbolsByName(result)["Acme\\Order"]; !ok {
		t.Errorf("expected symbol Acme\\Order in end-to-end extract")
	}
}

func TestOwnsFile(t *testing.T) {
	e := New()
	for _, f := range []string{"a.php", "tpl.phtml", "x.PHP"} {
		if !e.OwnsFile(f) {
			t.Errorf("OwnsFile(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"a.js", "b.go", "c.txt"} {
		if e.OwnsFile(f) {
			t.Errorf("OwnsFile(%q) = true, want false", f)
		}
	}
}

// writeFile creates dir/rel with the given content, making parent dirs as needed.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
