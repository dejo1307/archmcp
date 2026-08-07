package dotnetextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// Reduced from giraffe-fsharp/Giraffe's Routing.fs.
const giraffeFS = `namespace Giraffe

[<RequireQualifiedAccess>]
module SubRouting =
    open Microsoft.AspNetCore.Http

    [<Literal>]
    let private RouteKey = "giraffe_route"

    let getSavedPartialPath (ctx: HttpContext) =
        match ctx.Items.TryGetValue RouteKey with
        | true, route -> route |> string |> strOption
        | false, _ -> None

    let routeWithPartialPath (path: string) (handler: HttpHandler) : HttpHandler =
        fun (next: HttpFunc) (ctx: HttpContext) ->
            let savedPartialPath = getSavedPartialPath ctx
            ctx.Items.Remove RouteKey |> ignore
            handler next ctx
`

func TestFSharp_ModuleAndFreeFunctions(t *testing.T) {
	ff := scanFSharp(giraffeFS, "src/Giraffe/Routing.fs")

	m := symbolNamed(t, ff, "src/Giraffe.SubRouting")
	if m.Props["symbol_kind"] != facts.SymbolClass {
		t.Errorf("symbol_kind = %v, want class (a module compiles to a static class)", m.Props["symbol_kind"])
	}
	if m.Props["fsharp_module"] != true {
		t.Error("fsharp_module flag lost")
	}
	if m.Props["namespace"] != "Giraffe" {
		t.Errorf("namespace = %v", m.Props["namespace"])
	}

	// A module-level let with parameters is a FREE FUNCTION — the thing C# and VB
	// do not have, and the reason F# orphan findings are high-confidence.
	fn := symbolNamed(t, ff, "src/Giraffe.SubRouting.getSavedPartialPath")
	if fn.Props["symbol_kind"] != facts.SymbolFunc {
		t.Errorf("symbol_kind = %v, want function", fn.Props["symbol_kind"])
	}
	symbolNamed(t, ff, "src/Giraffe.SubRouting.routeWithPartialPath")
}

// `let private RouteKey = "…"` has no parameters: a value, not a function.
func TestFSharp_ValueBindingIsNotAFunction(t *testing.T) {
	ff := scanFSharp(giraffeFS, "src/Giraffe/Routing.fs")
	v := symbolNamed(t, ff, "src/Giraffe.SubRouting.RouteKey")
	if v.Props["symbol_kind"] == facts.SymbolFunc {
		t.Error("a parameterless let is a value, not a function")
	}
	if v.Props["exported"] != false {
		t.Errorf("exported = %v, want false for `let private`", v.Props["exported"])
	}
}

func TestFSharp_OpensBecomeDependencies(t *testing.T) {
	ff := scanFSharp(giraffeFS, "src/Giraffe/Routing.fs")
	for _, f := range ff {
		if f.Kind == facts.KindDependency && f.Props["import"] == "Microsoft.AspNetCore.Http" {
			return
		}
	}
	t.Error("open did not become a dependency")
}

// One function calling another in the same module is the edge that keeps it from
// reading as dead.
func TestFSharp_BodyReferences(t *testing.T) {
	ff := scanFSharp(giraffeFS, "src/Giraffe/Routing.fs")
	calls := relTargets(symbolNamed(t, ff, "src/Giraffe.SubRouting.routeWithPartialPath"), facts.RelCalls)
	if !has(calls, "getSavedPartialPath") {
		t.Errorf("intra-module call lost; got %v", calls)
	}
	if !has(calls, "Remove") {
		t.Errorf("member access lost; got %v", calls)
	}
}

// Scope is closed by DEDENT, not by a keyword. A second module at the same column
// must not nest inside the first.
func TestFSharp_DedentClosesScope(t *testing.T) {
	ff := scanFSharp(`namespace N

module A =
    let inA () = 1

module B =
    let inB () = 2
`, "src/x/M.fs")
	symbolNamed(t, ff, "src/x.A.inA")
	symbolNamed(t, ff, "src/x.B.inB")
	for _, f := range ff {
		if f.Name == "src/x.A.B" || f.Name == "src/x.A.B.inB" {
			t.Errorf("module B nested inside A: %q", f.Name)
		}
	}
}

// A `let` inside a type is private state, not a member — the same rule the C# and
// VB walkers apply to private fields.
func TestFSharp_LetInsideTypeIsNotAMember(t *testing.T) {
	ff := scanFSharp(`namespace N

type Cache() =
    let mutable store = 0
    member this.Value = store
`, "src/x/C.fs")
	symbolNamed(t, ff, "src/x.Cache")
	symbolNamed(t, ff, "src/x.Cache.Value")
	for _, f := range ff {
		if f.Name == "src/x.Cache.store" {
			t.Error("a let inside a type is private state, not a symbol")
		}
	}
}

func TestFSharp_TypeInterfaceAndInherit(t *testing.T) {
	ff := scanFSharp(`namespace N

type Widget() =
    inherit BaseWidget()
    interface IDisposable with
        member this.Dispose() = ()
`, "src/x/W.fs")
	impl := relTargets(symbolNamed(t, ff, "src/x.Widget"), facts.RelImplements)
	if !has(impl, "BaseWidget") || !has(impl, "IDisposable") {
		t.Errorf("implements = %v, want BaseWidget and IDisposable", impl)
	}
}

// `//` inside a string is not a comment, and (* *) blocks nest.
func TestFSharp_CommentsAndStrings(t *testing.T) {
	ff := scanFSharp(`namespace N

module M =
    let url () = "http://example.com/path"
    (* let hidden () = Ghost.Call()
       (* nested *) *)
    let after () = Real.Call()
`, "src/x/M.fs")
	symbolNamed(t, ff, "src/x.M.url")
	symbolNamed(t, ff, "src/x.M.after")
	for _, f := range ff {
		if f.Name == "src/x.M.hidden" {
			t.Error("a commented-out binding must not become a symbol")
		}
	}
	if !has(relTargets(symbolNamed(t, ff, "src/x.M.after"), facts.RelCalls), "Real") {
		t.Error("code after a nested block comment was lost")
	}
}

func TestFSharp_Complexity(t *testing.T) {
	ff := scanFSharp(`namespace N

module M =
    let classify x =
        match x with
        | Some v when v > 0 -> "pos"
        | Some _ -> "neg"
        | None -> "none"
`, "src/x/M.fs")
	m := symbolNamed(t, ff, "src/x.M.classify")
	if c, _ := m.Props["cyclomatic"].(int); c < 3 {
		t.Errorf("cyclomatic = %v, want >= 3 (match arms are decisions)", m.Props["cyclomatic"])
	}
}

func TestFSharp_FileExtensions(t *testing.T) {
	for _, f := range []string{"a/b.fs", "a/b.fsi", "a/b.fsx"} {
		if !isFSharpFile(f) {
			t.Errorf("%s should be F#", f)
		}
	}
	if isFSharpFile("a/b.fsproj") {
		t.Error(".fsproj is an MSBuild project, not a source file")
	}
}
