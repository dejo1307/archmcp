package javaextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// extractAll writes the given files to a temp repo and runs the full extractor
// (including the two-pass canonicalization), returning all emitted facts.
func extractAll(t *testing.T, files map[string]string) []facts.Fact {
	t.Helper()
	dir := t.TempDir()
	var relFiles []string
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		relFiles = append(relFiles, rel)
	}
	ff, err := New().Extract(context.Background(), dir, relFiles)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return ff
}

func findFact(ff []facts.Fact, name string) (facts.Fact, bool) {
	for _, f := range ff {
		if f.Name == name {
			return f, true
		}
	}
	return facts.Fact{}, false
}

func findFactKind(ff []facts.Fact, kind, name string) (facts.Fact, bool) {
	for _, f := range ff {
		if f.Kind == kind && f.Name == name {
			return f, true
		}
	}
	return facts.Fact{}, false
}

func factsByKind(ff []facts.Fact, kind string) []facts.Fact {
	var out []facts.Fact
	for _, f := range ff {
		if f.Kind == kind {
			out = append(out, f)
		}
	}
	return out
}

func hasRelation(f facts.Fact, kind, target string) bool {
	for _, r := range f.Relations {
		if r.Kind == kind && r.Target == target {
			return true
		}
	}
	return false
}

func TestExtract_ClassMethodField(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/main/java/pkg/Order.java": `package pkg;

public class Order {
    private final double total;
    public static final int MAX = 100;

    public Order(double total) {
        this.total = total;
    }

    public double calculate() {
        return total * 1.2;
    }
}
`,
	})

	cls, ok := findFact(ff, "src/main/java/pkg.Order")
	if !ok {
		t.Fatalf("missing class fact; got %v", names(ff))
	}
	if cls.Props["symbol_kind"] != facts.SymbolClass {
		t.Errorf("Order symbol_kind = %v, want class", cls.Props["symbol_kind"])
	}
	if cls.Props["exported"] != true {
		t.Errorf("Order exported = %v, want true", cls.Props["exported"])
	}
	if cls.Props["fqn"] != "pkg.Order" {
		t.Errorf("Order fqn = %v, want pkg.Order", cls.Props["fqn"])
	}

	m, ok := findFact(ff, "src/main/java/pkg.Order.calculate")
	if !ok {
		t.Fatalf("missing method fact; got %v", names(ff))
	}
	if m.Props["symbol_kind"] != facts.SymbolMethod {
		t.Errorf("calculate symbol_kind = %v, want method", m.Props["symbol_kind"])
	}
	if m.Props["receiver"] != "Order" {
		t.Errorf("calculate receiver = %v, want Order", m.Props["receiver"])
	}

	field, ok := findFact(ff, "src/main/java/pkg.Order.total")
	if !ok {
		t.Fatalf("missing field fact total; got %v", names(ff))
	}
	if field.Props["symbol_kind"] != facts.SymbolConstant {
		t.Errorf("total symbol_kind = %v, want constant (final)", field.Props["symbol_kind"])
	}
}

func TestExtract_InterfaceEnumRecord(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"a/Shape.java": "package a;\npublic interface Shape { double area(); }\n",
		"a/Color.java": "package a;\npublic enum Color { RED, GREEN, BLUE }\n",
		"a/Point.java": "package a;\npublic record Point(int x, int y) {}\n",
	})

	iface, _ := findFact(ff, "a.Shape")
	if iface.Props["symbol_kind"] != facts.SymbolInterface {
		t.Errorf("Shape symbol_kind = %v, want interface", iface.Props["symbol_kind"])
	}
	en, _ := findFact(ff, "a.Color")
	if en.Props["symbol_kind"] != facts.SymbolEnum {
		t.Errorf("Color symbol_kind = %v, want enum", en.Props["symbol_kind"])
	}
	rec, ok := findFact(ff, "a.Point")
	if !ok || rec.Props["record"] != true {
		t.Errorf("Point record fact missing or not marked record: %+v", rec.Props)
	}
}

func TestExtract_ImplementsExtends(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"p/Animal.java": "package p;\npublic abstract class Animal {}\n",
		"p/Pet.java":    "package p;\npublic interface Pet {}\n",
		"p/Dog.java": `package p;

public class Dog extends Animal implements Pet {}
`,
	})

	dog, ok := findFact(ff, "p.Dog")
	if !ok {
		t.Fatalf("missing Dog; got %v", names(ff))
	}
	// Same-package supertypes resolve to canonical "<dir>.<Type>" names.
	if !hasRelation(dog, facts.RelImplements, "p.Animal") {
		t.Errorf("Dog should implement/extend p.Animal; got %+v", dog.Relations)
	}
	if !hasRelation(dog, facts.RelImplements, "p.Pet") {
		t.Errorf("Dog should implement p.Pet; got %+v", dog.Relations)
	}
}

func TestExtract_ImportsInternalVsExternal(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"app/svc/Service.java": `package app.svc;

import app.data.Repo;
import java.util.List;

public class Service {
    private Repo repo;
}
`,
		"app/data/Repo.java": "package app.data;\npublic class Repo {}\n",
	})

	var internalOK, externalOK bool
	for _, f := range factsByKind(ff, facts.KindDependency) {
		switch f.Props["import"] {
		case "app.data.Repo":
			if f.Props["source"] == "internal" && hasRelation(f, facts.RelImports, "app/data") {
				internalOK = true
			}
		case "java.util.List":
			if f.Props["source"] == "external" {
				externalOK = true
			}
		}
	}
	if !internalOK {
		t.Error("app.data.Repo import should be internal and target module app/data")
	}
	if !externalOK {
		t.Error("java.util.List import should be external")
	}
}

// TestExtract_StaticImportResolvesInternal covers the parent-FQN fallback:
// a static member import names the member, not the type, so the declaring type's
// FQN is the parent of the import string.
func TestExtract_StaticImportResolvesInternal(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"app/svc/Service.java": `package app.svc;

import static app.data.Constants.MAX;

public class Service {
    int v = MAX;
}
`,
		"app/data/Constants.java": "package app.data;\npublic class Constants { public static final int MAX = 1; }\n",
	})

	var ok bool
	for _, f := range factsByKind(ff, facts.KindDependency) {
		if f.Props["import"] == "app.data.Constants.MAX" {
			if f.Props["source"] == "internal" && hasRelation(f, facts.RelImports, "app/data") {
				ok = true
			}
		}
	}
	if !ok {
		t.Error("static import app.data.Constants.MAX should resolve internal to module app/data")
	}
}

// TestExtract_UnindexedTypeResolvesViaPackage covers the second fallback branch:
// an imported type we didn't index as a top-level class still resolves to its
// package's module dir, because the package is internal.
func TestExtract_UnindexedTypeResolvesViaPackage(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"app/svc/Service.java": `package app.svc;

import app.data.Repo.Inner;

public class Service {}
`,
		"app/data/Repo.java": "package app.data;\npublic class Repo { public static class Inner {} }\n",
	})

	var ok bool
	for _, f := range factsByKind(ff, facts.KindDependency) {
		if f.Props["import"] == "app.data.Repo.Inner" {
			// Resolves via parent type app.data.Repo (or package app.data) → app/data.
			if f.Props["source"] == "internal" && hasRelation(f, facts.RelImports, "app/data") {
				ok = true
			}
		}
	}
	if !ok {
		t.Error("import of un-indexed type app.data.Repo.Inner should resolve internal to app/data")
	}
}

// TestExtract_WildcardNotOverResolved guards that the parent-FQN fallback is NOT
// applied to wildcard imports: an external wildcard stays external (it must not
// walk to a grandparent), while an internal wildcard still resolves normally.
func TestExtract_WildcardNotOverResolved(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"app/svc/Service.java": `package app.svc;

import app.data.*;
import com.external.lib.*;

public class Service {}
`,
		"app/data/Repo.java": "package app.data;\npublic class Repo {}\n",
	})

	var internalWildcardOK, externalWildcardExternal = false, true
	for _, f := range factsByKind(ff, facts.KindDependency) {
		switch f.Props["import"] {
		case "app.data":
			if f.Props["source"] == "internal" && hasRelation(f, facts.RelImports, "app/data") {
				internalWildcardOK = true
			}
		case "com.external.lib":
			if f.Props["source"] != "external" {
				externalWildcardExternal = false
			}
		}
	}
	if !internalWildcardOK {
		t.Error("internal wildcard import app.data.* should resolve to app/data")
	}
	if !externalWildcardExternal {
		t.Error("external wildcard import com.external.lib.* must stay external (no grandparent fallback)")
	}
}

func TestExtract_InstantiatesAndCalls(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"m/Widget.java": "package m;\npublic class Widget {}\n",
		"m/Factory.java": `package m;

public class Factory {
    public Widget make() {
        helper();
        return new Widget();
    }

    private void helper() {}
}
`,
	})

	mk, ok := findFact(ff, "m.Factory.make")
	if !ok {
		t.Fatalf("missing Factory.make; got %v", names(ff))
	}
	if !hasRelation(mk, facts.RelInstantiates, "m.Widget") {
		t.Errorf("make should instantiate m.Widget; got %+v", mk.Relations)
	}
	if !hasRelation(mk, facts.RelCalls, "m.Factory.helper") {
		t.Errorf("make should call m.Factory.helper; got %+v", mk.Relations)
	}
}

func TestExtract_JdkBuiltinsSuppressed(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"x/Foo.java": `package x;

public class Foo {
    public void run() {
        String s = new String("hi");
        Object o = new Object();
    }
}
`,
	})
	run, _ := findFact(ff, "x.Foo.run")
	for _, r := range run.Relations {
		if r.Kind == facts.RelInstantiates && (r.Target == "String" || r.Target == "Object" ||
			r.Target == "java.lang.String" || r.Target == "x.String") {
			t.Errorf("java.lang type should not produce instantiate edge: %+v", r)
		}
	}
}

func TestExtract_NestedTypeQualified(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"n/Outer.java": `package n;

public class Outer {
    public static class Inner {
        public void ping() {}
    }
}
`,
	})
	if _, ok := findFact(ff, "n.Outer.Inner"); !ok {
		t.Errorf("missing nested type n.Outer.Inner; got %v", names(ff))
	}
	if _, ok := findFact(ff, "n.Outer.Inner.ping"); !ok {
		t.Errorf("missing nested method n.Outer.Inner.ping; got %v", names(ff))
	}
}

func TestExtract_ModuleFacts(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"a/A.java": "package a;\npublic class A {}\n",
		"b/B.java": "package b;\npublic class B {}\n",
	})
	for _, dir := range []string{"a", "b"} {
		m, ok := findFactKind(ff, facts.KindModule, dir)
		if !ok {
			t.Errorf("missing module fact %q", dir)
			continue
		}
		if m.Props["language"] != "java" {
			t.Errorf("module %q language = %v", dir, m.Props["language"])
		}
	}
}

func names(ff []facts.Fact) []string {
	var out []string
	for _, f := range ff {
		out = append(out, f.Kind+":"+f.Name)
	}
	return out
}
