package scalaextractor

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
	scala "github.com/tree-sitter/tree-sitter-scala/bindings/go"
)

// TestGrammarSmoke is the ABI guard, and it is the most important test in this
// package.
//
// tree-sitter-scala v0.25.0 and later are generated against tree-sitter ABI 15,
// while the vendored go-tree-sitter runtime accepts at most 14. The rejection is
// SILENT: SetLanguage fails, every file parses to nothing, and the result is
// indistinguishable from a repository that contains no Scala. That is exactly how
// the C# grammar failed once, which is why the dependency is pinned to v0.24.1 —
// the newest ABI-14 release — and why this test asserts the pin still holds rather
// than trusting the go.mod line to stay put through a routine `go get -u`.
//
// If this fails after a dependency bump, the fix is to pin the grammar back, not
// to loosen the assertion.
func TestGrammarSmoke(t *testing.T) {
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(scala.Language())); err != nil {
		t.Fatalf("SetLanguage failed — the grammar is almost certainly built against a "+
			"newer tree-sitter ABI than the vendored runtime accepts. Pin "+
			"tree-sitter-scala to v0.24.1. Error: %v", err)
	}

	// Both dialects, because they exercise different halves of the grammar and a
	// version that dropped one would otherwise pass on the other.
	for _, tc := range []struct{ name, src string }{
		{"scala2", "package a.b\n\nclass Foo(x: Int) extends Bar {\n  def run(): Unit = println(x)\n}\n"},
		{"scala3", "package a.b\n\nenum Color:\n  case Red, Green\n\ntrait Store[F[_]]:\n  def get(id: Long): F[String]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tree := parser.Parse([]byte(tc.src), nil)
			defer tree.Close()
			root := tree.RootNode()
			if root == nil {
				t.Fatal("nil root node")
			}
			if root.HasError() {
				t.Fatalf("a trivial valid file parsed with errors — grammar mismatch:\n%s", root.ToSexp())
			}
		})
	}
}

// TestWalkerNodeKindsStillExist pins the exact grammar node kinds the walker
// dispatches on. A grammar upgrade that renames one would otherwise degrade
// extraction silently — the walker's default branch descends into anything it does
// not recognize, so a renamed `object_definition` would stop producing symbols
// without producing a single error.
func TestWalkerNodeKindsStillExist(t *testing.T) {
	src := `package a.b

import c.d.E
import c.d.{F, G => H}

class Klass extends E
case class Rec(x: Int)
object Obj
case object CObj
trait Trait { def m(): Int }
enum Enum:
  case One

given Ordering[Int] with
  def compare(a: Int, b: Int): Int = 0

extension (s: String) def shout: String = s

sealed abstract class Modified

object Holder {
  type Alias = Int
  val v = 1
  var mut = 2
  def f(): Int = 1
  private def hidden(): Int = 2
  val made = new Klass()
}
`
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(sitter.NewLanguage(scala.Language())); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := parser.Parse([]byte(src), nil)
	defer tree.Close()

	seen := map[string]bool{}
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.IsNamed() {
			seen[n.Kind()] = true
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())

	// Every kind the walker's switch branches on, and the field-bearing nodes it
	// reads through ChildByFieldName.
	for _, kind := range []string{
		"package_clause", "import_declaration", "namespace_selectors",
		"arrow_renamed_identifier", "class_definition", "object_definition",
		"trait_definition", "enum_definition", "simple_enum_case",
		"function_definition", "function_declaration", "val_definition",
		"var_definition", "type_definition", "given_definition",
		"extension_definition", "instance_expression", "extends_clause",
		"modifiers", "template_body",
	} {
		if !seen[kind] {
			t.Errorf("grammar no longer produces node kind %q — the walker dispatches on it "+
				"and would silently stop extracting", kind)
		}
	}
}
