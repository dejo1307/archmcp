package rubyextractor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dejo1307/archmcp/internal/facts"
)

func TestExtractFile_BasicClassAndMethod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "order.rb")
	src := `# frozen_string_literal: true

module Orders
  class Order < ApplicationRecord
    def total
      items.sum(:price)
    end

    def self.recent
      where("created_at > ?", 1.day.ago)
    end
  end
end
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	result := extractFile(f, "packages/orders/app/models/order.rb", true, false)

	// Collect by kind and name.
	byName := make(map[string]facts.Fact)
	for _, fact := range result {
		byName[fact.Name] = fact
	}

	// Module Orders.
	mod, ok := byName["Orders"]
	if !ok {
		t.Fatal("missing module Orders")
	}
	if mod.Kind != facts.KindSymbol {
		t.Errorf("Orders kind = %q, want symbol", mod.Kind)
	}
	sk, _ := mod.Props["symbol_kind"].(string)
	if sk != facts.SymbolInterface {
		t.Errorf("Orders symbol_kind = %q, want interface", sk)
	}

	// Class Orders::Order.
	cls, ok := byName["Orders::Order"]
	if !ok {
		t.Fatal("missing class Orders::Order")
	}
	if cls.Kind != facts.KindSymbol {
		t.Errorf("Orders::Order kind = %q, want symbol", cls.Kind)
	}
	sk, _ = cls.Props["symbol_kind"].(string)
	if sk != facts.SymbolClass {
		t.Errorf("Orders::Order symbol_kind = %q, want class", sk)
	}
	superclass, _ := cls.Props["superclass"].(string)
	if superclass != "ApplicationRecord" {
		t.Errorf("superclass = %q, want ApplicationRecord", superclass)
	}
	// Should have implements relation to ApplicationRecord.
	hasImpl := false
	for _, r := range cls.Relations {
		if r.Kind == facts.RelImplements && r.Target == "ApplicationRecord" {
			hasImpl = true
		}
	}
	if !hasImpl {
		t.Error("Orders::Order missing implements relation to ApplicationRecord")
	}

	// Instance method Orders::Order#total.
	meth, ok := byName["Orders::Order#total"]
	if !ok {
		t.Fatal("missing method Orders::Order#total")
	}
	sk, _ = meth.Props["symbol_kind"].(string)
	if sk != facts.SymbolMethod {
		t.Errorf("total symbol_kind = %q, want method", sk)
	}

	// Class method Orders::Order.recent.
	cmeth, ok := byName["Orders::Order.recent"]
	if !ok {
		t.Fatal("missing class method Orders::Order.recent")
	}
	sk, _ = cmeth.Props["symbol_kind"].(string)
	if sk != facts.SymbolFunc {
		t.Errorf("recent symbol_kind = %q, want function", sk)
	}
}

func TestStorageFacts_DeclaresTargetIsDirectory(t *testing.T) {
	relFile := "packages/items/app/models/item.rb"

	fileFacts := []facts.Fact{
		{
			Kind: facts.KindSymbol,
			Name: "Item",
			File: relFile,
			Line: 3,
			Props: map[string]any{
				"symbol_kind": facts.SymbolClass,
				"superclass":  "ApplicationRecord",
				"language":    "ruby",
			},
		},
	}

	result := extractStorageFacts(relFile, fileFacts)
	if len(result) == 0 {
		t.Fatal("expected at least one storage fact")
	}

	storageFact := result[0]
	if storageFact.Name != "Item" {
		t.Errorf("storage fact name = %q, want Item", storageFact.Name)
	}

	// The declares target must be the directory, not the class name.
	if len(storageFact.Relations) == 0 {
		t.Fatal("storage fact has no relations")
	}
	declTarget := storageFact.Relations[0].Target
	want := "packages/items/app/models"
	if declTarget != want {
		t.Errorf("declares target = %q, want %q", declTarget, want)
	}
	if declTarget == "Item" {
		t.Error("declares target must not be the class name (self-loop)")
	}
}

func TestAssociationFactNames_IncludeFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "order.rb")
	src := `class Order < ApplicationRecord
  belongs_to :user
  has_many :items
end
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	relFile := "packages/orders/app/models/order.rb"
	result := extractAssociationsFromFile(path, relFile)

	if len(result) == 0 {
		t.Fatal("expected association facts")
	}

	for _, fact := range result {
		if fact.Kind != facts.KindDependency {
			continue
		}
		if !strings.HasPrefix(fact.Name, relFile+":") {
			t.Errorf("association fact name %q should start with file path %q", fact.Name, relFile+":")
		}
	}

	// Verify specific associations.
	names := make(map[string]bool)
	for _, fact := range result {
		names[fact.Name] = true
	}
	if !names[relFile+":belongs_to :user"] {
		t.Error("missing belongs_to :user with file prefix")
	}
	if !names[relFile+":has_many :items"] {
		t.Error("missing has_many :items with file prefix")
	}
}

func TestPackwerk_RootDependencyNormalization(t *testing.T) {
	dir := t.TempDir()

	// Create packwerk.yml.
	packwerkYml := `package_paths:
  - "."
  - "packages/*"
`
	if err := os.WriteFile(filepath.Join(dir, "packwerk.yml"), []byte(packwerkYml), 0o644); err != nil {
		t.Fatal(err)
	}

	// Root package.yml.
	rootPkg := `enforce_dependencies: true
`
	if err := os.WriteFile(filepath.Join(dir, "package.yml"), []byte(rootPkg), 0o644); err != nil {
		t.Fatal(err)
	}

	// A sub-package that depends on root (".").
	pkgDir := filepath.Join(dir, "packages", "orders")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ordersPkg := `enforce_dependencies: true
dependencies:
  - "."
  - "packages/payments"
`
	if err := os.WriteFile(filepath.Join(pkgDir, "package.yml"), []byte(ordersPkg), 0o644); err != nil {
		t.Fatal(err)
	}

	info := parsePackwerk(dir)
	if !info.detected {
		t.Fatal("packwerk should be detected")
	}

	// Find the orders module fact.
	var ordersFact *facts.Fact
	for i, f := range info.facts {
		if f.Name == "packages/orders" {
			ordersFact = &info.facts[i]
			break
		}
	}
	if ordersFact == nil {
		t.Fatal("missing packages/orders module fact")
	}

	// The dependency on "." should be normalized to "root".
	hasDotTarget := false
	hasRootTarget := false
	for _, r := range ordersFact.Relations {
		if r.Kind == facts.RelDependsOn {
			if r.Target == "." {
				hasDotTarget = true
			}
			if r.Target == "root" {
				hasRootTarget = true
			}
		}
	}
	if hasDotTarget {
		t.Error("dependency target '.' should have been normalized to 'root'")
	}
	if !hasRootTarget {
		t.Error("expected dependency target 'root' after normalization")
	}

	// The root module should be named "root", not ".".
	var rootFact *facts.Fact
	for i, f := range info.facts {
		if f.Name == "root" {
			rootFact = &info.facts[i]
			break
		}
	}
	if rootFact == nil {
		t.Fatal("missing root module fact (should be named 'root', not '.')")
	}
}
