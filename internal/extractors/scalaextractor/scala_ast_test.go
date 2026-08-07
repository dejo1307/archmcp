package scalaextractor

import (
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// extractAST runs the walker directly on a source string, with no package index,
// so a test asserts what one file produces before cross-file resolution.
func extractAST(t *testing.T, relFile, src string) []facts.Fact {
	t.Helper()
	return extractFileAST([]byte(src), relFile, map[string]string{})
}

// findFact returns the fact with the given name, failing the test if absent.
func findFact(t *testing.T, ff []facts.Fact, name string) facts.Fact {
	t.Helper()
	for _, f := range ff {
		if f.Name == name {
			return f
		}
	}
	var got []string
	for _, f := range ff {
		got = append(got, f.Kind+" "+f.Name)
	}
	sort.Strings(got)
	t.Fatalf("no fact named %q; have:\n  %s", name, strings.Join(got, "\n  "))
	return facts.Fact{}
}

func hasRelation(f facts.Fact, kind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == kind && r.Target == target {
			return true
		}
	}
	return false
}

func TestSymbolKinds(t *testing.T) {
	src := `package com.example.model

sealed abstract class Animal(val name: String)
case class Dog(name: String, age: Int) extends Animal
case object Cat extends Animal
trait Greeter { def greet(n: String): String }
object Registry {
  type Id = Long
  val cache = 1
  var hits = 0
  def register(a: Animal): Unit = ()
  object Nested { def deep(): Int = 1 }
}
`
	ff := extractAST(t, "src/main/scala/com/example/model/Animal.scala", src)
	dir := "src/main/scala/com/example/model"

	cases := []struct {
		name, kind string
	}{
		{dir + ".Animal", facts.SymbolClass},
		{dir + ".Dog", facts.SymbolClass},
		{dir + ".Cat", facts.SymbolClass},
		{dir + ".Greeter", facts.SymbolInterface},
		{dir + ".Greeter.greet", facts.SymbolMethod},
		{dir + ".Registry", facts.SymbolClass},
		{dir + ".Registry.Id", facts.SymbolType},
		{dir + ".Registry.cache", facts.SymbolConstant},
		{dir + ".Registry.hits", facts.SymbolVariable},
		{dir + ".Registry.register", facts.SymbolMethod},
		{dir + ".Registry.Nested", facts.SymbolClass},
		{dir + ".Registry.Nested.deep", facts.SymbolMethod},
	}
	for _, tc := range cases {
		f := findFact(t, ff, tc.name)
		if got := f.Props["symbol_kind"]; got != tc.kind {
			t.Errorf("%s: symbol_kind = %v, want %v", tc.name, got, tc.kind)
		}
		if !hasRelation(f, facts.RelDeclares, dir) {
			t.Errorf("%s: missing declares -> %s", tc.name, dir)
		}
	}

	// A `case class` and a `case object` are both marked, and an object says it is
	// a singleton without needing a symbol_kind outside the shared vocabulary.
	if got := findFact(t, ff, dir+".Dog").Props["case_class"]; got != true {
		t.Errorf("Dog: case_class = %v, want true", got)
	}
	if got := findFact(t, ff, dir+".Cat").Props["scala_object"]; got != true {
		t.Errorf("Cat: scala_object = %v, want true", got)
	}
	if got := findFact(t, ff, dir+".Animal").Props["sealed"]; got != true {
		t.Errorf("Animal: sealed = %v, want true", got)
	}
	if got := findFact(t, ff, dir+".Animal").Props["abstract"]; got != true {
		t.Errorf("Animal: abstract = %v, want true", got)
	}
	// A method is attributed to the type that declares it, not to the file.
	if got := findFact(t, ff, dir+".Registry.register").Props["receiver"]; got != "Registry" {
		t.Errorf("register: receiver = %v, want Registry", got)
	}
	// A nested object's members qualify through BOTH enclosing names.
	if got := findFact(t, ff, dir+".Registry.Nested.deep").Props["receiver"]; got != "Nested" {
		t.Errorf("deep: receiver = %v, want Nested", got)
	}
}

func TestFQNTracksPackageNotDirectory(t *testing.T) {
	// Scala, like C#, lets a package disagree with the file system. The fact NAME
	// is directory-anchored (every other language's convention) while the fqn
	// follows the package — which is what a reference in another file resolves
	// against, and the reason both are recorded.
	src := `package com.example.core

class Base
`
	ff := extractAST(t, "weird/path/Base.scala", src)
	f := findFact(t, ff, "weird/path.Base")
	if got := f.Props["fqn"]; got != "com.example.core.Base" {
		t.Errorf("fqn = %v, want com.example.core.Base", got)
	}
}

func TestChainedPackageClauses(t *testing.T) {
	// `package com.example` followed by `package model` names ONE package,
	// com.example.model. Reading only the first would mis-resolve every reference
	// into it.
	src := `package com.example
package model

class Order
`
	ff := extractAST(t, "src/Order.scala", src)
	if got := findFact(t, ff, "src.Order").Props["fqn"]; got != "com.example.model.Order" {
		t.Errorf("fqn = %v, want com.example.model.Order", got)
	}
}

func TestExtendsAndMixinsBecomeImplements(t *testing.T) {
	src := `package com.example.svc

import com.example.core.Base
import com.example.util.{Logging => L}

class Service extends Base with Runnable with L {
  def run(): Unit = ()
}
`
	ff := extractAST(t, "src/Service.scala", src)
	f := findFact(t, ff, "src.Service")

	// Resolved through the file's imports...
	if !hasRelation(f, facts.RelImplements, "com.example.core.Base") {
		t.Errorf("missing implements -> com.example.core.Base; got %+v", f.Relations)
	}
	// ...through a RENAMED import, keyed on the local name the code writes...
	if !hasRelation(f, facts.RelImplements, "com.example.util.Logging") {
		t.Errorf("missing implements -> com.example.util.Logging (renamed import); got %+v", f.Relations)
	}
	// ...while an unimported bare name stays BARE. The walker does not qualify it
	// with the file's package: Scala auto-imports scala.*, java.lang.* and
	// scala.Predef.*, so `Runnable` here is java.lang.Runnable, and publishing
	// `com.example.svc.Runnable` would name a type that package never declared.
	// canonicalizeTargets resolves the genuine same-package case afterwards, where
	// it can check the merged fact set (see TestSamePackageResolvedOnlyWhenDeclared).
	if !hasRelation(f, facts.RelImplements, "Runnable") {
		t.Errorf("expected bare implements -> Runnable; got %+v", f.Relations)
	}
}

func TestImportForms(t *testing.T) {
	src := `package com.example.app

import java.time.Instant
import scala.concurrent.Future
import com.example.core.{Base, Helper => H}
import com.example.legacy._
import com.example.modern.*
`
	ff := extractAST(t, "src/App.scala", src)

	want := map[string]string{
		"java.time.Instant":       "stdlib",
		"scala.concurrent.Future": "stdlib",
		"com.example.core.Base":   "external", // promoted to internal by pass 2
		"com.example.core.Helper": "external",
		"com.example.legacy":      "external",
		"com.example.modern":      "external",
	}
	got := map[string]string{}
	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		imp, _ := f.Props["import"].(string)
		src, _ := f.Props[facts.PropSource].(string)
		got[imp] = src
	}
	for imp, wantSrc := range want {
		if got[imp] != wantSrc {
			t.Errorf("import %s: source = %q, want %q (all: %v)", imp, got[imp], wantSrc, got)
		}
	}
	// Both wildcard spellings are recognized as wildcards, so resolveImport does
	// not walk to their parent package.
	for _, f := range ff {
		if f.Kind == facts.KindDependency && f.Props["import"] == "com.example.legacy" {
			if f.Props["wildcard"] != true {
				t.Errorf("Scala 2 `_` wildcard not flagged: %+v", f.Props)
			}
		}
		if f.Kind == facts.KindDependency && f.Props["import"] == "com.example.modern" {
			if f.Props["wildcard"] != true {
				t.Errorf("Scala 3 `*` wildcard not flagged: %+v", f.Props)
			}
		}
	}
}

func TestNewBecomesInstantiates(t *testing.T) {
	src := `package com.example.app

import com.example.db.UserRepo

class Service {
  val repo = new UserRepo()
  def build(): Thing = new Thing(repo)
}
`
	ff := extractAST(t, "src/Service.scala", src)
	if f := findFact(t, ff, "src.Service.repo"); !hasRelation(f, facts.RelInstantiates, "com.example.db.UserRepo") {
		t.Errorf("val initializer: missing instantiates -> com.example.db.UserRepo; got %+v", f.Relations)
	}
	// An unimported constructor target stays bare for the same reason.
	if f := findFact(t, ff, "src.Service.build"); !hasRelation(f, facts.RelInstantiates, "Thing") {
		t.Errorf("method body: expected bare instantiates -> Thing; got %+v", f.Relations)
	}
}

func TestExportedVisibility(t *testing.T) {
	src := `package com.example.app

class Public
private class Hidden
protected class Guarded
object Holder {
  private[app] val packagePrivate = 1
  def open(): Int = 1
  private def closed(): Int = 2
}
`
	ff := extractAST(t, "src/V.scala", src)
	cases := map[string]bool{
		"src.Public":                true,
		"src.Hidden":                false,
		"src.Guarded":               false,
		"src.Holder.packagePrivate": false, // private[app] is package-private
		"src.Holder.open":           true,
		"src.Holder.closed":         false,
	}
	for name, want := range cases {
		if got := findFact(t, ff, name).Props["exported"]; got != want {
			t.Errorf("%s: exported = %v, want %v", name, got, want)
		}
	}
}

func TestScala3Constructs(t *testing.T) {
	src := `package com.example.api

enum Color:
  case Red, Green, Blue

enum Status(val code: Int):
  case Active extends Status(1)

given Ordering[Color] with
  def compare(a: Color, b: Color): Int = 0

extension (s: String)
  def shout: String = s.toUpperCase

trait Store[F[_]]:
  def get(id: Long): F[Option[String]]
`
	ff := extractAST(t, "src/Api.scala", src)

	if got := findFact(t, ff, "src.Color").Props["symbol_kind"]; got != facts.SymbolEnum {
		t.Errorf("Color: symbol_kind = %v, want enum", got)
	}
	// Every case in a comma-separated list becomes its own member.
	for _, c := range []string{"Red", "Green", "Blue"} {
		f := findFact(t, ff, "src.Color."+c)
		if f.Props["enum_case"] != true {
			t.Errorf("%s: enum_case = %v, want true", c, f.Props["enum_case"])
		}
	}
	// A case with a parent names it, the same implements edge a class produces.
	// Bare at this stage; pass 2 binds it to the enum declared in the same package.
	if f := findFact(t, ff, "src.Status.Active"); !hasRelation(f, facts.RelImplements, "Status") {
		t.Errorf("Status.Active: missing implements -> Status; got %+v", f.Relations)
	}
	// An anonymous given is named for the type it provides and records that type.
	g := findFact(t, ff, "src.given_Ordering")
	if g.Props["scala_given"] != true {
		t.Errorf("given: scala_given = %v, want true", g.Props["scala_given"])
	}
	if !hasRelation(g, facts.RelImplements, "Ordering") {
		t.Errorf("given: missing implements -> Ordering; got %+v", g.Relations)
	}
	// Its body declares a real method, qualified through the given.
	if got := findFact(t, ff, "src.given_Ordering.compare").Props["symbol_kind"]; got != facts.SymbolMethod {
		t.Errorf("given body: compare symbol_kind = %v, want method", got)
	}
	// An extension method is tagged rather than read as a plain free function.
	if got := findFact(t, ff, "src.shout").Props["scala_extension"]; got != true {
		t.Errorf("shout: scala_extension = %v, want true", got)
	}
	// A braceless (significant-indentation) trait body still yields its members.
	if got := findFact(t, ff, "src.Store.get").Props["abstract"]; got != true {
		t.Errorf("Store.get: abstract = %v, want true", got)
	}
}

func TestDestructuringValEmitsNoSymbol(t *testing.T) {
	// `val (a, b) = pair` binds a pattern. Emitting a symbol for it would require
	// guessing which name the fact should carry, so the binding is skipped — but
	// its initializer is still walked, so edges inside it survive.
	src := `package com.example.app

import com.example.db.Pair

object Holder {
  val (left, right) = new Pair()
}
`
	ff := extractAST(t, "src/H.scala", src)
	for _, f := range ff {
		if f.Kind == facts.KindSymbol && (strings.HasSuffix(f.Name, ".left") || strings.HasSuffix(f.Name, ".right")) {
			t.Errorf("destructuring binding emitted a symbol: %s", f.Name)
		}
	}
	if f := findFact(t, ff, "src.Holder"); !hasRelation(f, facts.RelInstantiates, "com.example.db.Pair") {
		t.Errorf("initializer edge lost; got %+v", f.Relations)
	}
}
