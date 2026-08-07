package scalaextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func testRefTargets(t *testing.T, relFile, src string) map[string]bool {
	t.Helper()
	ff := testRefsFromFile([]byte(src), relFile)
	out := map[string]bool{}
	for _, f := range ff {
		for _, r := range f.Relations {
			out[r.Target] = true
		}
	}
	return out
}

// TestTestRefsCaptureProductionReferences pins what this pass is for: a production
// symbol whose only caller is a spec keeps an inbound edge and stops reading as dead.
func TestTestRefsCaptureProductionReferences(t *testing.T) {
	src := `package com.example.app

import org.scalatest.flatspec.AnyFlatSpec
import org.scalatest.matchers.should.Matchers

class OrderServiceSpec extends AnyFlatSpec with Matchers {
  "OrderService" should "find an order" in {
    val svc = new OrderService(repo)
    val found = svc.findById(1L)
    found shouldBe defined
    OrderRepo.clear()
  }
}
`
	got := testRefTargets(t, "src/test/scala/OrderServiceSpec.scala", src)
	for _, want := range []string{
		"OrderService",    // constructed
		"findById",        // called on a local: the bare name is what matches
		"OrderRepo.clear", // qualified call on an object
		"clear",           // and its bare form
	} {
		if !got[want] {
			t.Errorf("missing reference %q; have %v", want, keysOf(got))
		}
	}
}

// TestTestRefsDropHarnessNoise keeps the reference set about the subject rather than
// the framework, across the four spec dialects the corpus uses.
func TestTestRefsDropHarnessNoise(t *testing.T) {
	src := `package com.example.app

import munit.FunSuite

class Spec extends FunSuite {
  test("adds") {
    assertEquals(Calculator.add(1, 2), 3)
    assert(Calculator.isReady)
    Mockito.verify(dep).close()
    forAll(Gen.posNum[Int]) { n => succeed }
  }
}
`
	got := testRefTargets(t, "src/test/scala/Spec.scala", src)
	for _, noise := range []string{
		"test", "assert", "assertEquals", "forAll", "succeed",
		"Mockito.verify", "Gen.posNum", "posNum",
	} {
		if got[noise] {
			t.Errorf("harness name %q recorded as a production reference; have %v", noise, keysOf(got))
		}
	}
	// The subject survives.
	for _, want := range []string{"Calculator.add", "add", "Calculator.isReady"} {
		if !got[want] {
			t.Errorf("missing production reference %q; have %v", want, keysOf(got))
		}
	}
}

// TestHarnessReceiverDisqualifiesTheBareName is the C# lesson restated for Scala, and
// it is the one filter here that affects correctness rather than precision.
// Production code really does declare `verify` and `equals`; if the harness's own
// calls contributed those bare names, a genuine dead-code finding would be silently
// suppressed.
func TestHarnessReceiverDisqualifiesTheBareName(t *testing.T) {
	src := `package p

class S extends AnyFlatSpec {
  "x" should "y" in {
    Mockito.verify(dep)
    Assert.equals(a, b)
  }
}
`
	got := testRefTargets(t, "src/test/scala/S.scala", src)
	for _, bad := range []string{"verify", "equals"} {
		if got[bad] {
			t.Errorf("bare %q leaked from a harness receiver; have %v", bad, keysOf(got))
		}
	}
}

// TestTestRefsEmitOnlySymbolFreeReferenceFacts pins the contract that keeps test code
// out of the graph: no symbols, no modules, no routes, and a fact name that cannot
// collide with a symbol.
func TestTestRefsEmitOnlySymbolFreeReferenceFacts(t *testing.T) {
	src := `package p

import org.scalatest.flatspec.AnyFlatSpec

class BigSpec extends AnyFlatSpec {
  val helper = new Helper()
  def setup(): Unit = Service.boot()
  "x" should "y" in { Service.run() }
}
`
	ff := testRefsFromFile([]byte(src), "src/test/scala/BigSpec.scala")
	if len(ff) != 1 {
		t.Fatalf("want exactly one fact per file, got %d", len(ff))
	}
	f := ff[0]
	if f.Kind != facts.KindTestRef {
		t.Errorf("kind = %q, want %q", f.Kind, facts.KindTestRef)
	}
	if f.Name != "src/test/scala/BigSpec.scala" {
		t.Errorf("name = %q, want the file path (it must never equal a symbol name)", f.Name)
	}
	for _, r := range f.Relations {
		if r.Kind != facts.RelCalls {
			t.Errorf("relation kind = %q, want only %q", r.Kind, facts.RelCalls)
		}
	}
}

// TestTestRefsAreDeterministic guards the sort: the target set is built in a map, and
// facts.jsonl is hashed into the snapshot id.
func TestTestRefsAreDeterministic(t *testing.T) {
	src := `package p

class S extends AnyFlatSpec {
  "x" should "y" in {
    Alpha.one(); Beta.two(); Gamma.three(); Delta.four(); Epsilon.five()
  }
}
`
	first := testRefsFromFile([]byte(src), "src/test/scala/S.scala")
	for i := 0; i < 30; i++ {
		again := testRefsFromFile([]byte(src), "src/test/scala/S.scala")
		if len(again) != len(first) {
			t.Fatalf("fact count varies between runs")
		}
		for j := range first[0].Relations {
			if first[0].Relations[j] != again[0].Relations[j] {
				t.Fatalf("target order varies between runs: %v vs %v",
					first[0].Relations, again[0].Relations)
			}
		}
	}
}

// TestExtractTestRefsEndToEnd drives the plugin entry point over real files.
func TestExtractTestRefsEndToEnd(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/test/scala/OrderSpec.scala", "package p\nclass OrderSpec { def t() = OrderService.find(1) }\n")
	write("src/test/scala/notes.md", "not scala\n")

	ff, err := (&ScalaExtractor{}).ExtractTestRefs(context.Background(), root,
		[]string{"src/test/scala/OrderSpec.scala", "src/test/scala/notes.md"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ff) != 1 {
		t.Fatalf("want one fact (the .scala file only), got %d", len(ff))
	}
	found := false
	for _, r := range ff[0].Relations {
		if r.Target == "OrderService.find" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing OrderService.find; got %+v", ff[0].Relations)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
