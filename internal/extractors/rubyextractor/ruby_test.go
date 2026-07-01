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

// TestExtractFile_BareConstantReferences checks that a constant used as a value
// (registered, passed as an argument, matched in case/when, in an array) is
// recorded as a RelCalls edge so the referenced class/module is not mis-reported
// as dead code. scope_resolution paths are recorded whole.
func TestExtractFile_BareConstantReferences(t *testing.T) {
	src := `class Registry
  def wire
    register(MyJob)
    handlers = [FooHandler, BarHandler]
    klass = Chat::Message
    case obj
    when SomeError
      retry
    end
  end
end
`
	result := extractFileAST([]byte(src), "app/services/registry.rb", false, true)
	meth, ok := symbolsByName(result)["Registry#wire"]
	if !ok {
		t.Fatal("missing method Registry#wire")
	}
	for _, want := range []string{"MyJob", "FooHandler", "BarHandler", "Chat::Message", "SomeError"} {
		if !hasCall(meth, want) {
			t.Errorf("missing bare-constant RelCalls -> %s; relations = %v", want, meth.Relations)
		}
	}
	// The bare constant target carries no ".", so it is not a coupling-graph
	// "Recv.method" form — guard that we did not accidentally emit one.
	if hasCall(meth, "MyJob.register") {
		t.Errorf("bare constant must not become a Recv.method target; relations = %v", meth.Relations)
	}
}

// TestExtractFile_ConstantReceiverCallStillQualified checks that the bare-constant
// capture does not regress the qualified-call form: a Const.method call must still
// produce the "Const.method" edge (for coupling), now alongside a bare "Const" one.
func TestExtractFile_ConstantReceiverCallStillQualified(t *testing.T) {
	src := `class Builder
  def run(ids)
    Items::Facade.fetch(ids)
  end
end
`
	result := extractFileAST([]byte(src), "app/services/builder.rb", false, true)
	meth := symbolsByName(result)["Builder#run"]
	if !hasCall(meth, "Items::Facade.fetch") {
		t.Errorf("qualified call edge lost; relations = %v", meth.Relations)
	}
	if !hasCall(meth, "Items::Facade") {
		t.Errorf("missing bare-receiver constant edge; relations = %v", meth.Relations)
	}
}

// TestExtractFile_BuiltinConstantsSkipped checks that bare references to Ruby
// core/stdlib constants are not emitted as call edges (they inflate fan-in on
// monkey-patch reopenings), while application constants still are.
func TestExtractFile_BuiltinConstantsSkipped(t *testing.T) {
	src := `class Worker
  def run
    Array.new
    x = [String, Time]
    enqueue(MyJob)
  end
end
`
	result := extractFileAST([]byte(src), "app/services/worker.rb", false, true)
	meth := symbolsByName(result)["Worker#run"]
	for _, skip := range []string{"Array", "String", "Time"} {
		if hasCall(meth, skip) {
			t.Errorf("builtin constant %s must not be emitted as a call edge; relations = %v", skip, meth.Relations)
		}
	}
	if !hasCall(meth, "MyJob") {
		t.Errorf("application constant MyJob should still be recorded; relations = %v", meth.Relations)
	}
}

// TestExtractFile_SerializerAttributeFold checks that a serializer's attribute and
// association DSL folds the backing methods (and the include_<name>? predicate) in
// as references on the serializer class, so they are not mis-reported as dead.
func TestExtractFile_SerializerAttributeFold(t *testing.T) {
	src := `class PostSerializer < ApplicationSerializer
  attributes :cooked, :score
  has_one :user
  def cooked
    object.cooked
  end
  def include_score?
    scope.admin?
  end
end
`
	result := extractFileAST([]byte(src), "app/serializers/post_serializer.rb", true, true)
	cls, ok := symbolsByName(result)["PostSerializer"]
	if !ok {
		t.Fatal("missing class PostSerializer")
	}
	for _, want := range []string{"cooked", "include_cooked?", "score", "include_score?", "user", "include_user?"} {
		if !hasCall(cls, want) {
			t.Errorf("missing serializer DSL fold RelCalls -> %s; relations = %v", want, cls.Relations)
		}
	}
}

// TestExtractFile_NonSerializerHasManyUnaffected guards that has_many on a model
// (not a serializer) still produces an association dependency fact and is NOT
// short-circuited by the serializer fold.
func TestExtractFile_NonSerializerHasManyUnaffected(t *testing.T) {
	src := `class User < ApplicationRecord
  has_many :posts
end
`
	result := extractFileAST([]byte(src), "app/models/user.rb", true, true)
	var sawAssoc bool
	for _, f := range result {
		if f.Kind == facts.KindDependency {
			for _, r := range f.Relations {
				if r.Kind == facts.RelDependsOn && r.Target == "Post" {
					sawAssoc = true
				}
			}
		}
	}
	if !sawAssoc {
		t.Error("model has_many :posts should still emit a depends_on association fact (serializer fold must not swallow it)")
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

// testRefFact returns the single KindTestRef fact from extractTestRefsAST output.
func testRefFact(result []facts.Fact) (facts.Fact, bool) {
	for _, f := range result {
		if f.Kind == facts.KindTestRef {
			return f, true
		}
	}
	return facts.Fact{}, false
}

// TestExtractFile_CustomClassMacroRecorded checks that a bare-receiver class-body
// call (a custom Rails DSL macro like `requires_login` / `cluster_concurrency`)
// records the MACRO NAME as a use on the enclosing class, so the base-class method
// backing the macro is not mis-reported as dead. The target must be bare (no "."),
// so it never pollutes the packwerk coupling graph.
func TestExtractFile_CustomClassMacroRecorded(t *testing.T) {
	src := `class ReportsController < ApplicationController
  requires_login except: [:index]
  cluster_concurrency 1
end
`
	result := extractFileAST([]byte(src), "app/controllers/reports_controller.rb", true, true)
	cls, ok := symbolsByName(result)["ReportsController"]
	if !ok {
		t.Fatal("missing class ReportsController")
	}
	for _, want := range []string{"requires_login", "cluster_concurrency"} {
		if !hasCall(cls, want) {
			t.Errorf("missing custom macro RelCalls -> %s; relations = %v", want, cls.Relations)
		}
	}
	if hasCall(cls, "ReportsController.requires_login") {
		t.Errorf("macro name must be bare, not a coupling form; relations = %v", cls.Relations)
	}
}

// TestExtractFile_StructuralMacrosNotRecordedAsCalls guards that structural
// keywords (require/private) are NOT recorded as macro call edges.
func TestExtractFile_StructuralMacrosNotRecordedAsCalls(t *testing.T) {
	src := `class Thing
  require "set"
  private
end
`
	result := extractFileAST([]byte(src), "app/models/thing.rb", false, true)
	cls := symbolsByName(result)["Thing"]
	for _, skip := range []string{"require", "private"} {
		if hasCall(cls, skip) {
			t.Errorf("structural keyword %s must not be a macro edge; relations = %v", skip, cls.Relations)
		}
	}
}

// TestExtractFile_TopLevelMacroNoSymbolEdge checks a body call outside any class
// attaches to no symbol fact (there is no enclosing class to hang it on).
func TestExtractFile_TopLevelMacroNoSymbolEdge(t *testing.T) {
	src := `configure_app foo: 1
`
	result := extractFileAST([]byte(src), "config/init.rb", false, true)
	for _, f := range result {
		if f.Kind == facts.KindSymbol && hasCall(f, "configure_app") {
			t.Errorf("top-level macro must not attach to any symbol fact; fact = %v", f)
		}
	}
}

// TestExtractFile_SelfAndSelfClassReceivers checks that `self.foo` and
// `self.class.bar` dispatch is resolved to the bare method name, while a bare
// `self.class` receiver does not emit a spurious `class` edge.
func TestExtractFile_SelfAndSelfClassReceivers(t *testing.T) {
	src := `class Job
  def run
    do_it if self.class.perform_when_readonly?
    self.reset
  end
end
`
	result := extractFileAST([]byte(src), "app/jobs/job.rb", false, true)
	meth := symbolsByName(result)["Job#run"]
	for _, want := range []string{"perform_when_readonly?", "reset"} {
		if !hasCall(meth, want) {
			t.Errorf("missing self-receiver RelCalls -> %s; relations = %v", want, meth.Relations)
		}
	}
	if hasCall(meth, "class") {
		t.Errorf("self.class receiver must not emit a bare 'class' edge; relations = %v", meth.Relations)
	}
	if hasCall(meth, "class.perform_when_readonly?") {
		t.Errorf("self.class.X must resolve to a bare method, not class.X; relations = %v", meth.Relations)
	}
}

// TestExtractFile_SelfBangAndPredicatePreserved checks predicate/bang suffixes
// survive self-receiver resolution.
func TestExtractFile_SelfBangAndPredicatePreserved(t *testing.T) {
	src := `class Model
  def process
    self.save!
    return unless self.valid?
  end
end
`
	result := extractFileAST([]byte(src), "app/models/model.rb", false, true)
	meth := symbolsByName(result)["Model#process"]
	for _, want := range []string{"save!", "valid?"} {
		if !hasCall(meth, want) {
			t.Errorf("missing self predicate/bang RelCalls -> %s; relations = %v", want, meth.Relations)
		}
	}
}

// TestExtractTestRefsAST checks the reference-only spec pass emits a single
// KindTestRef fact carrying the production symbols the spec exercises (qualified
// receivers fold to bare method via the collector's lastSeg), and NO symbol facts.
func TestExtractTestRefsAST(t *testing.T) {
	src := `# frozen_string_literal: true
describe Badge do
  it "computes ids" do
    expect(Badge.trust_level_badge_ids).to eq([1, 2])
    GlobalSetting.reset_redis_config!
  end
end
`
	result := extractTestRefsAST([]byte(src), "spec/services/badge_granter_spec.rb")
	for _, f := range result {
		if f.Kind == facts.KindSymbol {
			t.Fatalf("test-ref pass must not emit symbol facts; got %v", f)
		}
	}
	ref, ok := testRefFact(result)
	if !ok {
		t.Fatal("missing KindTestRef fact")
	}
	for _, want := range []string{"Badge.trust_level_badge_ids", "GlobalSetting.reset_redis_config!"} {
		if !hasCall(ref, want) {
			t.Errorf("missing test ref RelCalls -> %s; relations = %v", want, ref.Relations)
		}
	}
}

// fileRefFact returns the single KindFileRef fact from extractFileAST output, if any.
func fileRefFact(result []facts.Fact) (facts.Fact, bool) {
	for _, f := range result {
		if f.Kind == facts.KindFileRef {
			return f, true
		}
	}
	return facts.Fact{}, false
}

// countCalls returns how many RelCalls relations on a fact target the given name.
func countCalls(f facts.Fact, target string) int {
	n := 0
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls && r.Target == target {
			n++
		}
	}
	return n
}

// TestExtractFile_ClassBodySplatArgMethodCall checks that a method call in the
// ARGUMENT of a class-body macro (`requires_login *show_methods`) is captured on
// the class fact — not just the macro name — so the arg method is not flagged dead.
func TestExtractFile_ClassBodySplatArgMethodCall(t *testing.T) {
	src := `class TagsController < ApplicationController
  requires_login *show_methods
end
`
	result := extractFileAST([]byte(src), "app/controllers/tags_controller.rb", true, true)
	cls := symbolsByName(result)["TagsController"]
	for _, want := range []string{"requires_login", "show_methods"} {
		if !hasCall(cls, want) {
			t.Errorf("missing class-body RelCalls -> %s; relations = %v", want, cls.Relations)
		}
	}
	if hasCall(cls, "TagsController.show_methods") {
		t.Errorf("arg method must be bare, not a coupling form; relations = %v", cls.Relations)
	}
}

// TestExtractFile_ClassBodyQualifiedCall checks a qualified Const.method call in a
// class body attaches to the class fact (previously dropped by handleBodyCall).
func TestExtractFile_ClassBodyQualifiedCall(t *testing.T) {
	src := `class Report
  Badge.register(self)
end
`
	result := extractFileAST([]byte(src), "app/models/report.rb", false, true)
	cls := symbolsByName(result)["Report"]
	if !hasCall(cls, "Badge.register") {
		t.Errorf("missing class-body qualified RelCalls -> Badge.register; relations = %v", cls.Relations)
	}
}

// TestExtractFile_ClassBodyMacroNoDuplicate guards the Fix A / walkForCalls dedup:
// a class-body macro name is recorded exactly once on the class fact.
func TestExtractFile_ClassBodyMacroNoDuplicate(t *testing.T) {
	src := `class Thing
  requires_login except: [:index]
end
`
	result := extractFileAST([]byte(src), "app/controllers/thing_controller.rb", true, true)
	cls := symbolsByName(result)["Thing"]
	if got := countCalls(cls, "requires_login"); got != 1 {
		t.Errorf("requires_login recorded %d times, want exactly 1; relations = %v", got, cls.Relations)
	}
}

// TestExtractFile_TopLevelQualifiedCallOnFileRef checks a top-level (fixture-style)
// call is captured on a KindFileRef fact, leaks no symbol fact, and that a file with
// only ignorable top-level calls produces no file-ref fact.
func TestExtractFile_TopLevelQualifiedCallOnFileRef(t *testing.T) {
	result := extractFileAST([]byte("Badge.like_badge_counts(1, 2)\n"), "db/fixtures/006_badges.rb", false, true)
	fr, ok := fileRefFact(result)
	if !ok {
		t.Fatal("missing KindFileRef fact for a top-level call")
	}
	if !hasCall(fr, "Badge.like_badge_counts") {
		t.Errorf("missing file-ref RelCalls -> Badge.like_badge_counts; relations = %v", fr.Relations)
	}
	for _, f := range result {
		if f.Kind == facts.KindSymbol && hasCall(f, "Badge.like_badge_counts") {
			t.Errorf("top-level call must not attach to a symbol fact; fact = %v", f)
		}
	}
	// A file whose only top-level call is a suppressed keyword produces no file-ref.
	empty := extractFileAST([]byte("require \"set\"\n"), "config/init.rb", false, true)
	if _, ok := fileRefFact(empty); ok {
		t.Error("file with no real top-level refs should not emit a KindFileRef fact")
	}
}

// TestExtractFile_TopLevelAfterInitializeBlock checks a call inside a top-level
// plugin block (after_initialize do ... end) is captured on the file-ref fact.
func TestExtractFile_TopLevelAfterInitializeBlock(t *testing.T) {
	src := `after_initialize do
  CategoryList.register_included_association(:foo)
end
`
	result := extractFileAST([]byte(src), "plugins/foo/plugin.rb", true, true)
	fr, ok := fileRefFact(result)
	if !ok {
		t.Fatal("missing KindFileRef fact for a top-level block call")
	}
	if !hasCall(fr, "CategoryList.register_included_association") {
		t.Errorf("missing file-ref RelCalls -> CategoryList.register_included_association; relations = %v", fr.Relations)
	}
}

// TestExtractFile_TopLevelAssignmentRHS checks that a call on the RHS of a
// top-level assignment (a Rails initializer pattern) is captured on the file-ref
// fact — previously dropped because handleAssignment never walked the value.
func TestExtractFile_TopLevelAssignmentRHS(t *testing.T) {
	src := `if Rails.configuration.multisite
  assets_hostnames = GlobalSetting.cdn_hostnames
end
`
	result := extractFileAST([]byte(src), "config/initializers/200-first_middlewares.rb", true, true)
	fr, ok := fileRefFact(result)
	if !ok {
		t.Fatal("missing KindFileRef fact for a top-level assignment RHS call")
	}
	if !hasCall(fr, "GlobalSetting.cdn_hostnames") {
		t.Errorf("missing file-ref RelCalls -> GlobalSetting.cdn_hostnames; relations = %v", fr.Relations)
	}
	for _, f := range result {
		if f.Kind == facts.KindSymbol && hasCall(f, "GlobalSetting.cdn_hostnames") {
			t.Errorf("top-level assignment RHS call must not attach to a symbol fact; fact = %v", f)
		}
	}
}

// TestExtractFile_TopLevelSetterAssignmentRHS checks a call on the RHS of a
// top-level setter-assignment inside an if/else is captured.
func TestExtractFile_TopLevelSetterAssignmentRHS(t *testing.T) {
	src := `if Rails.env.test?
  MessageBus.configure(backend: :memory)
else
  MessageBus.redis_config = GlobalSetting.message_bus_redis_config
end
`
	result := extractFileAST([]byte(src), "config/initializers/004-message_bus.rb", true, true)
	fr, ok := fileRefFact(result)
	if !ok {
		t.Fatal("missing KindFileRef fact")
	}
	if !hasCall(fr, "GlobalSetting.message_bus_redis_config") {
		t.Errorf("missing file-ref RelCalls -> GlobalSetting.message_bus_redis_config; relations = %v", fr.Relations)
	}
}

// TestExtractFile_ClassBodyConstAssignmentProc checks that calls inside a Proc in
// a class-body constant assignment (Discourse's TYPE_FILTERS pattern) are captured
// on the class fact.
func TestExtractFile_ClassBodyConstAssignmentProc(t *testing.T) {
	src := `class GroupsController < ApplicationController
  TYPE_FILTERS = {
    my: Proc.new { |groups, user| Group.member_of(groups, user) },
    owner: Proc.new { |groups, user| Group.owner_of(groups, user) },
  }
end
`
	result := extractFileAST([]byte(src), "app/controllers/groups_controller.rb", true, true)
	cls := symbolsByName(result)["GroupsController"]
	for _, want := range []string{"Group.owner_of", "Group.member_of"} {
		if !hasCall(cls, want) {
			t.Errorf("missing class-body const-assignment Proc RelCalls -> %s; relations = %v", want, cls.Relations)
		}
	}
}

// fileRefPrefixes returns the dynamic_send_prefixes prop of the KindFileRef fact.
func fileRefPrefixes(result []facts.Fact) []string {
	for _, f := range result {
		if f.Kind == facts.KindFileRef {
			if raw, ok := f.Props["dynamic_send_prefixes"].([]string); ok {
				return raw
			}
		}
	}
	return nil
}

// TestExtractFile_InterpolatedSymbolPrefix checks that an interpolated symbol
// (`:"report_#{type}"`) — the mark of dynamic dispatch by computed name — records
// its static prefix on the file-scope fact.
func TestExtractFile_InterpolatedSymbolPrefix(t *testing.T) {
	src := `class IncomingLinksReport
  def self.find(type)
    report_method = :"report_#{type}"
    public_send(report_method, type)
  end
end
`
	result := extractFileAST([]byte(src), "app/models/incoming_links_report.rb", true, true)
	got := fileRefPrefixes(result)
	found := false
	for _, p := range got {
		if p == "report_" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected dynamic_send_prefixes to contain %q; got %v", "report_", got)
	}
}

// TestExtractFile_NoPrefixFromStaticSymbolOrString checks the heuristic does NOT
// fire for static symbols, too-short prefixes, or interpolated STRINGS (i18n keys).
func TestExtractFile_NoPrefixFromStaticSymbolOrString(t *testing.T) {
	src := `class Thing
  def go(type)
    a = :report_foo                 # static symbol, no interpolation
    b = :"m#{type}"                 # prefix too short / no underscore
    c = I18n.t("reports.#{type}.x") # interpolated STRING, not a symbol
    [a, b, c]
  end
end
`
	result := extractFileAST([]byte(src), "app/models/thing.rb", true, true)
	if got := fileRefPrefixes(result); len(got) != 0 {
		t.Errorf("expected no dynamic_send_prefixes, got %v", got)
	}
}

// TestExtractFile_SuperReferencesAncestor checks that a `super` call records a
// reference to the same-named ancestor method (the base an override delegates to).
func TestExtractFile_SuperReferencesAncestor(t *testing.T) {
	src := `module EE
  module IssuesFinder
    def negatable_params
      @negatable_params ||= super + [:weight]
    end
  end
end
`
	result := extractFileAST([]byte(src), "ee/app/finders/ee/issues_finder.rb", true, true)
	meth := symbolsByName(result)["EE::IssuesFinder#negatable_params"]
	if !hasCall(meth, "negatable_params") {
		t.Errorf("super should record a call to the same-named ancestor method; relations = %v", meth.Relations)
	}
}

// TestExtractFile_LiteralSymbolDispatch checks that a literal-symbol argument to a
// dispatcher (try/send/respond_to?) records a call to the named method, while a
// non-dispatcher method with a symbol arg (e.g. validates :name) does not.
func TestExtractFile_LiteralSymbolDispatch(t *testing.T) {
	src := `class BaseField
  def complexity(resolver)
    ext = resolver&.try(:calculate_ext_conn_complexity)
    v = @resolver_class.send(:requires_argument?)
    validates :name
    [ext, v]
  end
end
`
	result := extractFileAST([]byte(src), "app/graphql/types/base_field.rb", true, true)
	meth := symbolsByName(result)["BaseField#complexity"]
	for _, want := range []string{"calculate_ext_conn_complexity", "requires_argument?"} {
		if !hasCall(meth, want) {
			t.Errorf("dispatcher literal-symbol arg should record RelCalls -> %s; relations = %v", want, meth.Relations)
		}
	}
	// `validates :name` is a DSL, not a dispatcher — `name` must not be a call here.
	if hasCall(meth, "name") {
		t.Errorf("non-dispatcher symbol arg must not be recorded as a call; relations = %v", meth.Relations)
	}
}

// TestExtractFile_ChainedNoArgCall checks that a no-arg method call at the end of
// a chain (ActiveRecord scope / class-method chains) is captured, while common
// attribute/enumerable reads and single-level reads are not.
func TestExtractFile_ChainedNoArgCall(t *testing.T) {
	src := `class Worker
  def run
    DeployToken.active.with_owners.ordered_for_keyset_pagination
    merge_request.merge_request_closing_issues.preload_issue
    group_link.class.access_options
    user.name
    list.map.first
    a.b.count
  end
end
`
	result := extractFileAST([]byte(src), "app/workers/worker.rb", true, true)
	meth := symbolsByName(result)["Worker#run"]
	for _, want := range []string{"ordered_for_keyset_pagination", "preload_issue", "access_options"} {
		if !hasCall(meth, want) {
			t.Errorf("chained no-arg call should record RelCalls -> %s; relations = %v", want, meth.Relations)
		}
	}
	// Cheap chained reads (name/first/class are in rubyCheapMethods) and the
	// single-level read (user.name — var receiver) must NOT be recorded.
	for _, skip := range []string{"name", "first", "class"} {
		if hasCall(meth, skip) {
			t.Errorf("attribute/enumerable/single-level read %q must not be recorded as a chained call; relations = %v", skip, meth.Relations)
		}
	}
}

// TestIsRubyFile_Rake checks that .rake files and Rakefile are treated as Ruby.
func TestIsRubyFile_Rake(t *testing.T) {
	for _, p := range []string{"lib/tasks/gitlab/graphql_introspection.rake", "Rakefile"} {
		if !isRubyFile(p) {
			t.Errorf("isRubyFile(%q) = false, want true", p)
		}
	}
	if isRubyFile("app/models/foo.py") {
		t.Error("isRubyFile should be false for non-Ruby files")
	}
}

// TestExtractFile_RakeTaskCallCaptured checks a top-level call in a .rake file is
// recorded on the file-scope fact (so its target is not mis-reported as dead).
func TestExtractFile_RakeTaskCallCaptured(t *testing.T) {
	src := `namespace :gitlab do
  task introspection: :environment do
    puts CachedIntrospectionQuery.query_string_no_deprecated
  end
end
`
	result := extractFileAST([]byte(src), "lib/tasks/gitlab/graphql_introspection.rake", true, true)
	fr, ok := fileRefFact(result)
	if !ok || !hasCall(fr, "CachedIntrospectionQuery.query_string_no_deprecated") {
		t.Errorf("rake task call should be captured on the file-ref fact; result = %v", result)
	}
}
