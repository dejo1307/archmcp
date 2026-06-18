package rubyextractor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// symbolsByName indexes the symbol facts in a result by name (storage/dependency
// facts may share a class name, so they are excluded here).
func symbolsByName(result []facts.Fact) map[string]facts.Fact {
	m := make(map[string]facts.Fact)
	for _, f := range result {
		if f.Kind == facts.KindSymbol {
			m[f.Name] = f
		}
	}
	return m
}

// hasCall returns true if the fact has a RelCalls relation to target.
func hasCall(f facts.Fact, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls && r.Target == target {
			return true
		}
	}
	return false
}

func TestExtractFile_BasicClassAndMethod(t *testing.T) {
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
	result := extractFileAST([]byte(src), "packages/orders/app/models/order.rb", true, false)
	byName := symbolsByName(result)

	mod, ok := byName["Orders"]
	if !ok {
		t.Fatal("missing module Orders")
	}
	if sk, _ := mod.Props["symbol_kind"].(string); sk != facts.SymbolInterface {
		t.Errorf("Orders symbol_kind = %q, want interface", sk)
	}

	cls, ok := byName["Orders::Order"]
	if !ok {
		t.Fatal("missing class Orders::Order")
	}
	if sk, _ := cls.Props["symbol_kind"].(string); sk != facts.SymbolClass {
		t.Errorf("Orders::Order symbol_kind = %q, want class", sk)
	}
	if sc, _ := cls.Props["superclass"].(string); sc != "ApplicationRecord" {
		t.Errorf("superclass = %q, want ApplicationRecord", sc)
	}
	hasImpl := false
	for _, r := range cls.Relations {
		if r.Kind == facts.RelImplements && r.Target == "ApplicationRecord" {
			hasImpl = true
		}
	}
	if !hasImpl {
		t.Error("Orders::Order missing implements relation to ApplicationRecord")
	}

	meth, ok := byName["Orders::Order#total"]
	if !ok {
		t.Fatal("missing method Orders::Order#total")
	}
	if sk, _ := meth.Props["symbol_kind"].(string); sk != facts.SymbolMethod {
		t.Errorf("total symbol_kind = %q, want method", sk)
	}

	cmeth, ok := byName["Orders::Order.recent"]
	if !ok {
		t.Fatal("missing class method Orders::Order.recent")
	}
	if sk, _ := cmeth.Props["symbol_kind"].(string); sk != facts.SymbolFunc {
		t.Errorf("recent symbol_kind = %q, want function", sk)
	}
}

func TestStorageFacts_DeclaresTargetIsDirectory(t *testing.T) {
	relFile := "packages/items/app/models/item.rb"
	src := `class Item < ApplicationRecord
end
`
	result := extractFileAST([]byte(src), relFile, true, true)

	var storageFact *facts.Fact
	for i, f := range result {
		if f.Kind == facts.KindStorage && f.Name == "Item" {
			storageFact = &result[i]
			break
		}
	}
	if storageFact == nil {
		t.Fatal("expected a storage fact named Item")
	}
	if sk, _ := storageFact.Props["storage_kind"].(string); sk != "model" {
		t.Errorf("storage_kind = %q, want model", sk)
	}
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
	relFile := "packages/orders/app/models/order.rb"
	src := `class Order < ApplicationRecord
  belongs_to :user
  has_many :items
end
`
	result := extractFileAST([]byte(src), relFile, true, true)

	names := make(map[string]bool)
	for _, f := range result {
		if f.Kind != facts.KindDependency {
			continue
		}
		if _, ok := f.Props["association_kind"]; !ok {
			continue
		}
		if !strings.HasPrefix(f.Name, relFile+":") {
			t.Errorf("association fact name %q should start with file path %q", f.Name, relFile+":")
		}
		names[f.Name] = true
	}
	if !names[relFile+":belongs_to :user"] {
		t.Error("missing belongs_to :user with file prefix")
	}
	if !names[relFile+":has_many :items"] {
		t.Error("missing has_many :items with file prefix")
	}

	// has_many target is singularized + camelized; belongs_to is camelized as-is.
	for _, f := range result {
		if f.Name == relFile+":has_many :items" {
			if f.Relations[0].Target != "Item" {
				t.Errorf("has_many :items target = %q, want Item", f.Relations[0].Target)
			}
		}
		if f.Name == relFile+":belongs_to :user" {
			if f.Relations[0].Target != "User" {
				t.Errorf("belongs_to :user target = %q, want User", f.Relations[0].Target)
			}
		}
	}
}

// --- RelCalls extraction tests ---

func TestExtractFile_QualifiedClassMethodCall(t *testing.T) {
	src := `module Items
  class FetchService
    def call(ids)
      Items::Facade.fetch_item_fields(ids, ITEM_FIELDS)
    end
  end
end
`
	result := extractFileAST([]byte(src), "packages/items/app/services/fetch_service.rb", false, true)
	meth, ok := symbolsByName(result)["Items::FetchService#call"]
	if !ok {
		t.Fatal("missing method Items::FetchService#call")
	}
	if !hasCall(meth, "Items::Facade.fetch_item_fields") {
		t.Errorf("missing RelCalls -> Items::Facade.fetch_item_fields; relations = %v", meth.Relations)
	}
}

func TestExtractFile_MultiLevelNamespaceCall(t *testing.T) {
	src := `module HomepageSources
  class Builder
    def build(ids)
      HomepageSources::ItemDto.from_ids(ids)
    end
  end
end
`
	result := extractFileAST([]byte(src), "packages/homepage_sources/app/builder.rb", false, true)
	meth, ok := symbolsByName(result)["HomepageSources::Builder#build"]
	if !ok {
		t.Fatal("missing method HomepageSources::Builder#build")
	}
	if !hasCall(meth, "HomepageSources::ItemDto.from_ids") {
		t.Errorf("missing RelCalls -> HomepageSources::ItemDto.from_ids; relations = %v", meth.Relations)
	}
}

func TestExtractFile_ReceiverVariableCall(t *testing.T) {
	src := `class OrderProcessor
  def process(order)
    service.call(order)
  end
end
`
	result := extractFileAST([]byte(src), "app/models/order_processor.rb", false, true)
	meth, ok := symbolsByName(result)["OrderProcessor#process"]
	if !ok {
		t.Fatal("missing method OrderProcessor#process")
	}
	if !hasCall(meth, "service.call") {
		t.Errorf("missing RelCalls -> service.call; relations = %v", meth.Relations)
	}
}

func TestExtractFile_BareMethodCalls(t *testing.T) {
	src := `class PostsController
  def markdown_num(arg)
    render :json
    helper(arg)
    current_user
  end
end
`
	result := extractFileAST([]byte(src), "app/controllers/posts_controller.rb", true, true)
	meth, ok := symbolsByName(result)["PostsController#markdown_num"]
	if !ok {
		t.Fatal("missing method PostsController#markdown_num")
	}
	for _, want := range []string{"render", "helper", "current_user"} {
		if !hasCall(meth, want) {
			t.Errorf("missing bare RelCalls -> %s; relations = %v", want, meth.Relations)
		}
	}
	if hasCall(meth, "arg") {
		t.Errorf("parameter 'arg' must not be emitted as a call; relations = %v", meth.Relations)
	}
}

func TestExtractFile_BareCallSkipsLocalsAndKeywords(t *testing.T) {
	src := `class A
  def b
    x = compute
    x
    raise
    super
  end
end
`
	result := extractFileAST([]byte(src), "app/models/a.rb", false, true)
	meth := symbolsByName(result)["A#b"]
	if !hasCall(meth, "compute") {
		t.Errorf("missing RelCalls -> compute; relations = %v", meth.Relations)
	}
	for _, bad := range []string{"x", "raise", "super"} {
		if hasCall(meth, bad) {
			t.Errorf("%s must not be emitted as a call; relations = %v", bad, meth.Relations)
		}
	}
}

func TestExtractFile_RailsCallbackSymbolCalls(t *testing.T) {
	src := `class PostsController < ApplicationController
  before_action :authenticate_user!
  validate :check_something

  def authenticate_user!
  end
end
`
	result := extractFileAST([]byte(src), "app/controllers/posts_controller.rb", true, true)
	cls, ok := symbolsByName(result)["PostsController"]
	if !ok {
		t.Fatal("missing class PostsController")
	}
	for _, want := range []string{"authenticate_user!", "check_something"} {
		if !hasCall(cls, want) {
			t.Errorf("missing DSL RelCalls -> %s on class; relations = %v", want, cls.Relations)
		}
	}
}

func TestExtractFile_CallsDeduplication(t *testing.T) {
	src := `class Dispatcher
  def run(ids)
    Items::Facade.fetch_item_fields(ids, FIELDS)
    Items::Facade.fetch_item_fields(ids, OTHER_FIELDS)
  end
end
`
	result := extractFileAST([]byte(src), "app/dispatcher.rb", false, true)
	meth, ok := symbolsByName(result)["Dispatcher#run"]
	if !ok {
		t.Fatal("missing method Dispatcher#run")
	}
	count := 0
	for _, r := range meth.Relations {
		if r.Kind == facts.RelCalls && r.Target == "Items::Facade.fetch_item_fields" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 RelCalls edge, got %d", count)
	}
}

func TestExtractFile_TopLevelMethodCalls(t *testing.T) {
	// Ruby allows method calls without parentheses; the qualified tier must still
	// capture them.
	src := `def bootstrap
  Config.load_defaults
  Rails.application.initialize!
end
`
	result := extractFileAST([]byte(src), "config/init.rb", false, true)
	meth, ok := symbolsByName(result)["config.bootstrap"]
	if !ok {
		t.Fatal("missing top-level method config.bootstrap")
	}
	if !hasCall(meth, "Config.load_defaults") {
		t.Errorf("missing RelCalls -> Config.load_defaults; relations = %v", meth.Relations)
	}
	if !hasCall(meth, "Rails.application") {
		t.Errorf("missing RelCalls -> Rails.application; relations = %v", meth.Relations)
	}
}

func TestExtractFile_EndlessMethodCall(t *testing.T) {
	// Ruby 3.0+ endless method: def name(args) = Expr.call(args)
	src := `module HomepageSources
  class ItemDto
    ITEM_FIELDS = %i[id title].freeze

    def fields_by_id(item_ids) = Items::Facade.fetch_item_fields(item_ids, ITEM_FIELDS).index_by { |item| item[:id] }
  end
end
`
	result := extractFileAST([]byte(src), "packages/homepage_sources/app/public/homepage_sources/item_dto.rb", false, true)
	byName := symbolsByName(result)

	meth, ok := byName["HomepageSources::ItemDto#fields_by_id"]
	if !ok {
		t.Fatal("missing method HomepageSources::ItemDto#fields_by_id")
	}
	if !hasCall(meth, "Items::Facade.fetch_item_fields") {
		t.Errorf("missing RelCalls -> Items::Facade.fetch_item_fields; relations = %v", meth.Relations)
	}
	// The ALL-CAPS constant should be captured.
	if _, ok := byName["HomepageSources::ItemDto::ITEM_FIELDS"]; !ok {
		t.Error("missing constant HomepageSources::ItemDto::ITEM_FIELDS")
	}
}

func TestCallEdges_QualifiedAndReceiverAndChain(t *testing.T) {
	src := `class Logger
  def run(x)
    Items::Facade.fetch_item_fields(x)
    service.call(x)
    Rails.logger.info("msg")
  end
end
`
	result := extractFileAST([]byte(src), "app/logger.rb", false, true)
	meth := symbolsByName(result)["Logger#run"]
	for _, want := range []string{
		"Items::Facade.fetch_item_fields", // scope-resolution receiver
		"service.call",                    // lowercase receiver with args
		"Rails.logger",                    // qualified inner of a chain
		"logger.info",                     // chained receiver with args
	} {
		if !hasCall(meth, want) {
			t.Errorf("missing RelCalls -> %s; relations = %v", want, meth.Relations)
		}
	}
}

// --- AST-only coverage (cases the regex scanner handled poorly) ---

func TestAST_MultiLineCallArguments(t *testing.T) {
	src := `class Svc
  def run(ids)
    Items::Facade.fetch_item_fields(
      ids,
      FIELDS,
    )
  end
end
`
	result := extractFileAST([]byte(src), "app/svc.rb", false, true)
	meth := symbolsByName(result)["Svc#run"]
	if !hasCall(meth, "Items::Facade.fetch_item_fields") {
		t.Errorf("multi-line call not captured; relations = %v", meth.Relations)
	}
}

func TestAST_HeredocContainingEnd(t *testing.T) {
	// A heredoc body containing a bare "end" line used to corrupt the regex
	// depth counter; the grammar treats it as string content.
	src := "class Foo\n" +
		"  def bar\n" +
		"    sql = <<~SQL\n" +
		"      SELECT 1\n" +
		"      end\n" +
		"    SQL\n" +
		"    Other.call(sql)\n" +
		"  end\n" +
		"end\n"
	result := extractFileAST([]byte(src), "app/foo.rb", false, true)
	byName := symbolsByName(result)
	if _, ok := byName["Foo"]; !ok {
		t.Fatal("missing class Foo")
	}
	meth, ok := byName["Foo#bar"]
	if !ok {
		t.Fatal("missing method Foo#bar (heredoc likely broke scope tracking)")
	}
	if !hasCall(meth, "Other.call") {
		t.Errorf("call after heredoc not captured; relations = %v", meth.Relations)
	}
}

func TestAST_NestedModulesAndEigenclass(t *testing.T) {
	src := `module A
  module B
    class C
      class << self
        def build
        end
      end
    end
  end
end
`
	result := extractFileAST([]byte(src), "app/a.rb", false, true)
	byName := symbolsByName(result)
	if _, ok := byName["A::B::C"]; !ok {
		t.Fatal("missing deeply nested class A::B::C")
	}
	build, ok := byName["A::B::C.build"]
	if !ok {
		t.Fatal("missing eigenclass method A::B::C.build")
	}
	if sk, _ := build.Props["symbol_kind"].(string); sk != facts.SymbolFunc {
		t.Errorf("eigenclass method symbol_kind = %q, want func", sk)
	}
}

func TestAST_ConcernDetection(t *testing.T) {
	src := `module Trackable
  extend ActiveSupport::Concern
end
`
	result := extractFileAST([]byte(src), "app/models/concerns/trackable.rb", true, true)
	mod := symbolsByName(result)["Trackable"]
	if c, _ := mod.Props["concern"].(bool); !c {
		t.Errorf("Trackable should be flagged concern:true; props = %v", mod.Props)
	}
	// extend ActiveSupport::Concern must not be emitted as a mixin dependency.
	for _, f := range result {
		if f.Kind == facts.KindDependency {
			for _, r := range f.Relations {
				if r.Target == "ActiveSupport::Concern" {
					t.Error("ActiveSupport::Concern should not be a mixin dependency")
				}
			}
		}
	}
}

func TestAST_MixinsAndImports(t *testing.T) {
	src := `class Account < ApplicationRecord
  include Trackable
  prepend Auditable
  attr_accessor :name, :token
  require "set"
  require_relative "../helper"
end
`
	result := extractFileAST([]byte(src), "app/models/account.rb", true, true)

	var includeKind, prependKind, reqRel string
	attrs := map[string]bool{}
	imports := map[string]bool{}
	for _, f := range result {
		if f.Kind == facts.KindDependency {
			if mk, _ := f.Props["mixin_kind"].(string); mk != "" {
				if f.Relations[0].Target == "Trackable" {
					includeKind = mk
				}
				if f.Relations[0].Target == "Auditable" {
					prependKind = mk
				}
			}
			if rr, _ := f.Props["require_relative"].(bool); rr {
				reqRel = f.Relations[0].Target
			}
			for _, r := range f.Relations {
				if r.Kind == facts.RelImports {
					imports[r.Target] = true
				}
			}
		}
		if f.Kind == facts.KindSymbol {
			if ak, _ := f.Props["attr_kind"].(string); ak == "accessor" {
				attrs[f.Name] = true
			}
		}
	}
	if includeKind != "include" {
		t.Errorf("include mixin_kind = %q, want include", includeKind)
	}
	if prependKind != "prepend" {
		t.Errorf("prepend mixin_kind = %q, want prepend", prependKind)
	}
	if reqRel != "../helper" {
		t.Errorf("require_relative target = %q, want ../helper", reqRel)
	}
	if !imports["set"] {
		t.Error("missing require 'set' import")
	}
	if !attrs["Account#name"] || !attrs["Account#token"] {
		t.Errorf("missing attr_accessor symbols; got %v", attrs)
	}
}

// --- route tests ---

func TestRoutes_NestedNamespaceResourcesMember(t *testing.T) {
	src := `Rails.application.routes.draw do
  namespace :admin do
    resources :users, only: [:index, :show] do
      member do
        post "ban"
      end
    end
  end
  draw(:billing)
end
`
	result := parseRouteFileAST([]byte(src), "config/routes.rb")
	names := make(map[string]facts.Fact)
	for _, f := range result {
		if f.Kind == facts.KindRoute {
			names[f.Name] = f
		}
	}

	for _, want := range []string{"/admin/users", "/admin/users/:id", "/admin/users/:id/ban", "/billing"} {
		if _, ok := names[want]; !ok {
			t.Errorf("missing route %q; got %v", want, keys(names))
		}
	}
	// only: [:index, :show] must exclude create/new/edit/destroy.
	for _, absent := range []string{"/admin/users/new", "/admin/users/:id/edit"} {
		if _, ok := names[absent]; ok {
			t.Errorf("route %q should be excluded by only:", absent)
		}
	}
	if f, ok := names["/admin/users/:id/ban"]; ok {
		if m, _ := f.Props["method"].(string); m != "POST" {
			t.Errorf("ban route method = %q, want POST", m)
		}
	}
	if f, ok := names["/billing"]; ok {
		if m, _ := f.Props["method"].(string); m != "DRAW" {
			t.Errorf("draw route method = %q, want DRAW", m)
		}
	}
}

func TestRoutes_VerbWithHandler(t *testing.T) {
	src := `Rails.application.routes.draw do
  root to: "home#index"
  get "/health", to: "health#show"
end
`
	result := parseRouteFileAST([]byte(src), "config/routes.rb")
	byName := make(map[string]facts.Fact)
	for _, f := range result {
		byName[f.Name] = f
	}
	root, ok := byName["/"]
	if !ok {
		t.Fatal("missing root route")
	}
	if h, _ := root.Props["handler"].(string); h != "home#index" {
		t.Errorf("root handler = %q, want home#index", h)
	}
	health, ok := byName["/health"]
	if !ok {
		t.Fatal("missing /health route")
	}
	if h, _ := health.Props["handler"].(string); h != "health#show" {
		t.Errorf("/health handler = %q, want health#show", h)
	}
	if m, _ := health.Props["method"].(string); m != "GET" {
		t.Errorf("/health method = %q, want GET", m)
	}
}

func keys(m map[string]facts.Fact) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestPackwerk_RootDependencyNormalization(t *testing.T) {
	dir := t.TempDir()

	packwerkYml := `package_paths:
  - "."
  - "packages/*"
`
	if err := os.WriteFile(filepath.Join(dir, "packwerk.yml"), []byte(packwerkYml), 0o644); err != nil {
		t.Fatal(err)
	}
	rootPkg := `enforce_dependencies: true
`
	if err := os.WriteFile(filepath.Join(dir, "package.yml"), []byte(rootPkg), 0o644); err != nil {
		t.Fatal(err)
	}
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
