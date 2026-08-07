package csharpextractor

import (
	"sort"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// factByName finds a symbol fact by its canonical name.
func factByName(ff []facts.Fact, name string) *facts.Fact {
	for i := range ff {
		if ff[i].Name == name {
			return &ff[i]
		}
	}
	return nil
}

func hasRel(f *facts.Fact, kind, target string) bool {
	if f == nil {
		return false
	}
	for _, r := range f.Relations {
		if r.Kind == kind && r.Target == target {
			return true
		}
	}
	return false
}

func names(ff []facts.Fact, kind string) []string {
	var out []string
	for _, f := range ff {
		if f.Kind == kind {
			out = append(out, f.Name)
		}
	}
	return out
}

const orderServiceSrc = `
using System;
using System.Collections.Generic;
using System.Net.Http;
using Acme.Orders.Domain;
using Repo = Acme.Orders.Data.OrderRepository;

namespace Acme.Orders.Api;

public interface IOrderService
{
    Order Find(int id);
}

public sealed class OrderService : IOrderService, IDisposable
{
    private readonly Repo _repo;
    private readonly HttpClient _http;

    public const string CacheKey = "orders";
    private string _scratch;

    public OrderService(Repo repo, HttpClient http)
    {
        _repo = repo;
        _http = http;
    }

    public Order Find(int id)
    {
        return Lookup(id);
    }

    private Order Lookup(int id)
    {
        var payload = _http.GetStringAsync("/orders/" + id);
        return new Order(id, payload);
    }

    public void Dispose() { }
}
`

func TestExtract_TypesMembersAndEdges(t *testing.T) {
	ff := extractFileAST([]byte(orderServiceSrc), "src/Api/OrderService.cs")

	svc := factByName(ff, "src/Api.OrderService")
	if svc == nil {
		t.Fatalf("OrderService symbol missing; got %v", names(ff, facts.KindSymbol))
	}
	if svc.Props["symbol_kind"] != facts.SymbolClass {
		t.Errorf("symbol_kind = %v, want class", svc.Props["symbol_kind"])
	}
	if svc.Props["exported"] != true {
		t.Errorf("public class should be exported")
	}
	if svc.Props["sealed"] != true {
		t.Errorf("sealed modifier not captured")
	}
	if svc.Props["fqn"] != "Acme.Orders.Api.OrderService" {
		t.Errorf("fqn = %v, want Acme.Orders.Api.OrderService", svc.Props["fqn"])
	}
	if svc.Props["namespace"] != "Acme.Orders.Api" {
		t.Errorf("file-scoped namespace not applied: %v", svc.Props["namespace"])
	}

	// Base list becomes implements edges, for both the interface and IDisposable.
	if !hasRel(svc, facts.RelImplements, "IOrderService") {
		t.Errorf("missing implements IOrderService; got %v", svc.Relations)
	}
	if !hasRel(svc, facts.RelImplements, "IDisposable") {
		t.Errorf("missing implements IDisposable")
	}

	// The sole constructor's parameters are injected. The `Repo` alias must be
	// substituted for the type it names.
	if !hasRel(svc, facts.RelInjects, "Acme.Orders.Data.OrderRepository") {
		t.Errorf("alias not resolved in injects edge; got %v", svc.Relations)
	}
	if !hasRel(svc, facts.RelInjects, "HttpClient") {
		t.Errorf("missing injects HttpClient")
	}

	// A same-type bare call resolves to the sibling member.
	find := factByName(ff, "src/Api.OrderService.Find")
	if find == nil {
		t.Fatal("Find method missing")
	}
	if !hasRel(find, facts.RelCalls, "src/Api.OrderService.Lookup") {
		t.Errorf("bare same-type call not resolved; got %v", find.Relations)
	}

	// Only public/protected fields become symbols.
	if factByName(ff, "src/Api.OrderService.CacheKey") == nil {
		t.Error("public const should be a symbol")
	}
	if f := factByName(ff, "src/Api.OrderService._scratch"); f != nil {
		t.Error("private field should not be a symbol")
	}
	if f := factByName(ff, "src/Api.OrderService._repo"); f != nil {
		t.Error("private readonly field should not be a symbol")
	}

	// Interface members are public by default even with no modifier.
	ifind := factByName(ff, "src/Api.IOrderService.Find")
	if ifind == nil {
		t.Fatal("interface member missing")
	}
	if ifind.Props["exported"] != true {
		t.Error("interface member should default to exported")
	}

	// The I/O primitive seeds io_direct on the member that calls it.
	lookup := factByName(ff, "src/Api.OrderService.Lookup")
	if lookup == nil {
		t.Fatal("Lookup missing")
	}
	if lookup.Props["io_direct"] != true {
		t.Errorf("GetStringAsync should seed io_direct; got %v", lookup.Props)
	}
	if !hasRel(lookup, facts.RelInstantiates, "Order") {
		t.Errorf("missing instantiates Order; got %v", lookup.Relations)
	}
}

func TestExtract_UsingDirectives(t *testing.T) {
	ff := extractFileAST([]byte(orderServiceSrc), "src/Api/OrderService.cs")

	var imports []string
	for _, f := range ff {
		if f.Kind == facts.KindDependency {
			imports = append(imports, f.Props["import"].(string))
		}
	}
	want := []string{"System", "System.Collections.Generic", "System.Net.Http",
		"Acme.Orders.Domain", "Acme.Orders.Data.OrderRepository"}
	for _, w := range want {
		found := false
		for _, got := range imports {
			if got == w {
				found = true
			}
		}
		if !found {
			t.Errorf("missing using %q; got %v", w, imports)
		}
	}
}

const partialSrc1 = `
namespace Acme;
public partial class Widget
{
    public void A() { }
}
`

const partialSrc2 = `
namespace Acme;
public partial class Widget : IThing
{
    public void B() { }
}
`

func TestMergePartialTypes(t *testing.T) {
	ff := extractFileAST([]byte(partialSrc1), "src/Widget.A.cs")
	ff = append(ff, extractFileAST([]byte(partialSrc2), "src/Widget.B.cs")...)

	var before int
	for _, f := range ff {
		if f.Name == "src.Widget" {
			before++
		}
	}
	if before != 2 {
		t.Fatalf("expected 2 partial halves before merge, got %d", before)
	}

	merged := mergePartialTypes(ff)
	var got []facts.Fact
	for _, f := range merged {
		if f.Name == "src.Widget" {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("partial halves not merged: %d facts remain", len(got))
	}
	if got[0].File != "src/Widget.A.cs" {
		t.Errorf("survivor should be the earliest by file: got %s", got[0].File)
	}
	if got[0].Props["partial_declarations"] != 2 {
		t.Errorf("partial_declarations = %v, want 2", got[0].Props["partial_declarations"])
	}
	// The base list declared on the second half survives the merge.
	if !hasRel(&got[0], facts.RelImplements, "IThing") {
		t.Errorf("second half's implements edge lost; got %v", got[0].Relations)
	}
	// Both halves' methods remain distinct symbols.
	if factByName(merged, "src.Widget.A") == nil || factByName(merged, "src.Widget.B") == nil {
		t.Error("members of both halves should survive")
	}
}

func TestMergePartialTypes_OrderIndependent(t *testing.T) {
	a := extractFileAST([]byte(partialSrc1), "src/Widget.A.cs")
	b := extractFileAST([]byte(partialSrc2), "src/Widget.B.cs")

	fwd := mergePartialTypes(append(append([]facts.Fact{}, a...), b...))
	rev := mergePartialTypes(append(append([]facts.Fact{}, b...), a...))

	fw, rv := factByName(fwd, "src.Widget"), factByName(rev, "src.Widget")
	if fw == nil || rv == nil {
		t.Fatal("merged fact missing")
	}
	if fw.File != rv.File || fw.Line != rv.Line {
		t.Errorf("survivor depends on walk order: %s:%d vs %s:%d", fw.File, fw.Line, rv.File, rv.Line)
	}
	if len(fw.Relations) != len(rv.Relations) {
		t.Fatalf("relation count differs: %d vs %d", len(fw.Relations), len(rv.Relations))
	}
	for i := range fw.Relations {
		if fw.Relations[i] != rv.Relations[i] {
			t.Errorf("relation %d differs: %v vs %v", i, fw.Relations[i], rv.Relations[i])
		}
	}
}

const recordSrc = `
namespace Acme;

public record Money(decimal Amount, string Currency);

public record struct Point(int X, int Y);

public enum Status { Draft, Live }

public delegate void Notify(string message);

public static class StringExtensions
{
    public static string Slugify(this string value) => value.Trim();
}
`

func TestExtract_RecordsEnumsDelegatesExtensions(t *testing.T) {
	ff := extractFileAST([]byte(recordSrc), "src/Types.cs")

	money := factByName(ff, "src.Money")
	if money == nil || money.Props["symbol_kind"] != facts.SymbolClass {
		t.Errorf("record class should be a class symbol; got %v", money)
	}
	if money.Props["record"] != true {
		t.Error("record prop missing")
	}
	// Positional parameters are the compiler-generated public properties.
	if factByName(ff, "src.Money.Amount") == nil {
		t.Error("positional record property missing")
	}

	pt := factByName(ff, "src.Point")
	if pt == nil || pt.Props["symbol_kind"] != facts.SymbolStruct {
		t.Errorf("record struct should be a struct symbol; got %v", pt)
	}

	st := factByName(ff, "src.Status")
	if st == nil || st.Props["symbol_kind"] != facts.SymbolEnum {
		t.Errorf("enum symbol missing; got %v", st)
	}
	if factByName(ff, "src.Status.Draft") == nil {
		t.Error("enum member missing")
	}

	nf := factByName(ff, "src.Notify")
	if nf == nil || nf.Props["symbol_kind"] != facts.SymbolType {
		t.Errorf("delegate should be a type symbol; got %v", nf)
	}

	ext := factByName(ff, "src.StringExtensions.Slugify")
	if ext == nil {
		t.Fatal("extension method missing")
	}
	if ext.Props["extension_method"] != true || ext.Props["extends_type"] != "string" {
		t.Errorf("extension receiver not captured: %v", ext.Props)
	}
}

const complexitySrc = `
namespace Acme;
public class Calc
{
    public int Scan(List<int> items, int[] fixedSet)
    {
        var total = 0;
        for (var i = 0; i < 10; i++) { total += i; }
        foreach (var item in items)
        {
            if (item > 0 && item < 100) { total += Fetch(item); }
        }
        foreach (var f in fixedSet) { total += f; }
        return total;
    }

    private int Fetch(int id) => id;

    public int Recurse(int n) => n <= 1 ? 1 : Recurse(n - 1);
}
`

func TestExtract_ComplexityMetrics(t *testing.T) {
	ff := extractFileAST([]byte(complexitySrc), "src/Calc.cs")

	scan := factByName(ff, "src.Calc.Scan")
	if scan == nil {
		t.Fatal("Scan missing")
	}
	if scan.Props["loop_count"] != 3 {
		t.Errorf("loop_count = %v, want 3", scan.Props["loop_count"])
	}
	// A literal-bounded `for` and a foreach over a named array parameter differ:
	// only the latter grows with the input, so the scaling depth is 1, not 2.
	if scan.Props["loop_depth"] != 1 {
		t.Errorf("loop_depth = %v, want 1", scan.Props["loop_depth"])
	}
	if scan.Props["scaling_loop_depth"] != 1 {
		t.Errorf("scaling_loop_depth = %v, want 1", scan.Props["scaling_loop_depth"])
	}
	if c, _ := scan.Props["cyclomatic"].(int); c < 5 {
		t.Errorf("cyclomatic = %v, want >= 5 (3 loops + if + &&)", scan.Props["cyclomatic"])
	}
	calls, _ := scan.Props["calls_in_loop"].([]string)
	if len(calls) == 0 || !strings.Contains(strings.Join(calls, ","), "Fetch") {
		t.Errorf("in-loop call not recorded: %v", scan.Props["calls_in_loop"])
	}

	rec := factByName(ff, "src.Calc.Recurse")
	if rec == nil || rec.Props["recursive_self"] != true {
		t.Errorf("expression-bodied recursion not detected; got %v", rec)
	}
}

func TestExtract_TopLevelStatements(t *testing.T) {
	src := `
using System;
Console.WriteLine("hi");
Run();
`
	ff := extractFileAST([]byte(src), "src/Program.cs")
	ref := factByName(ff, "src/Program.cs")
	if ref == nil || ref.Kind != facts.KindFileRef {
		t.Fatalf("top-level statements should carry a file-scope reference; got %v", names(ff, facts.KindFileRef))
	}
}

func TestIsGeneratedSource(t *testing.T) {
	cases := []struct {
		file string
		src  string
		want bool
	}{
		{"src/Foo.g.cs", "class A {}", true},
		{"src/Foo.Designer.cs", "class A {}", true},
		{"src/AssemblyInfo.cs", "", true},
		{"src/Foo.cs", "// <auto-generated/>\nclass A {}", true},
		{"src/Foo.cs", "class A {}", false},
		{"src/Generator.cs", "class A { const string X = \"<auto-generated\"; }", true},
	}
	for _, c := range cases {
		if got := isGeneratedSource(c.file, []byte(c.src)); got != c.want {
			t.Errorf("isGeneratedSource(%q) = %v, want %v", c.file, got, c.want)
		}
	}
}

// TestBaseTypes_PunctuationIsNotAnEdge pins the fix for a phantom-node bug: the
// base list's `:` and `,` tokens are children like any other, and typeFullName
// renders a node as its own text — so walking every child emitted an `implements`
// edge to ":" for every type that declared a base at all.
func TestBaseTypes_PunctuationIsNotAnEdge(t *testing.T) {
	src := `
namespace Acme;
public class Multi : BaseOne, ITwo, IThree { }
`
	ff := extractFileAST([]byte(src), "src/Multi.cs")
	f := factByName(ff, "src.Multi")
	if f == nil {
		t.Fatal("Multi missing")
	}
	var targets []string
	for _, r := range f.Relations {
		if r.Kind == facts.RelImplements {
			targets = append(targets, r.Target)
		}
	}
	want := map[string]bool{"BaseOne": true, "ITwo": true, "IThree": true}
	if len(targets) != len(want) {
		t.Fatalf("implements targets = %v, want exactly %v", targets, want)
	}
	for _, tgt := range targets {
		if !want[tgt] {
			t.Errorf("unexpected implements target %q", tgt)
		}
	}
}

func TestIsIdentifierPath(t *testing.T) {
	ok := []string{"Foo", "Acme.Orders.Order", "_private", "@class", "T1"}
	bad := []string{"", ":", ",", "Foo,Bar", "Foo:", "1Foo", "Foo..Bar", "Foo<T>"}
	for _, s := range ok {
		if !isIdentifierPath(s) {
			t.Errorf("isIdentifierPath(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if isIdentifierPath(s) {
			t.Errorf("isIdentifierPath(%q) = true, want false", s)
		}
	}
}

// TestPartialSiblingCall_ResolvesAcrossHalves covers the call a per-file member
// index structurally cannot see: the two halves of a partial type are different
// parse trees, so a method in one calling a method in the other found nothing.
// The speculative target must survive when the member exists...
func TestPartialSiblingCall_ResolvesAcrossHalves(t *testing.T) {
	ff := extractFileAST([]byte(partialSrc1), "src/Widget.A.cs")
	ff = append(ff, extractFileAST([]byte(`
namespace Acme;
public partial class Widget
{
    public void B() { A(); }
}
`), "src/Widget.B.cs")...)
	ff = mergePartialTypes(ff)
	resolveCSharpTargets(ff)

	b := factByName(ff, "src.Widget.B")
	if !hasRel(b, facts.RelCalls, "src.Widget.A") {
		t.Errorf("cross-half call not resolved; got %v", b.Relations)
	}
}

// ...and must be dropped when it does not, so a base-class or extension-method
// call inside a partial type does not become an edge to a member nobody declared.
func TestPartialSiblingCall_UnknownMemberIsDropped(t *testing.T) {
	ff := extractFileAST([]byte(`
namespace Acme;
public partial class Widget
{
    public void B() { InheritedFromBase(); }
}
`), "src/Widget.B.cs")
	ff = mergePartialTypes(ff)
	resolveCSharpTargets(ff)

	b := factByName(ff, "src.Widget.B")
	if hasRel(b, facts.RelCalls, "src.Widget.InheritedFromBase") {
		t.Errorf("speculative call to an undeclared member survived: %v", b.Relations)
	}
}

// TestNonPartialType_DoesNotSpeculate is the control: outside a partial type an
// unknown bare call must draw no edge at all, since there is no second half it
// could be hiding in.
func TestNonPartialType_DoesNotSpeculate(t *testing.T) {
	ff := extractFileAST([]byte(`
namespace Acme;
public class Plain
{
    public void B() { InheritedFromBase(); }
}
`), "src/Plain.cs")
	b := factByName(ff, "src.Plain.B")
	for _, r := range b.Relations {
		if r.Kind == facts.RelCalls {
			t.Errorf("non-partial type speculated a call: %v", r)
		}
	}
}

// TestClassifyUsing_AliasNamesATypeNotANamespace pins that a `using X = Some.Type`
// and a `using static` resolve through the TYPE index. Read against the namespace
// index alone — which is all C#'s ordinary `using` needs — every one of them was
// filed as an external dependency.
func TestClassifyUsing_AliasResolvesThroughTypeIndex(t *testing.T) {
	ff := extractFileAST([]byte(`
using Repo = Acme.Data.OrderRepository;
namespace Acme.Api;
public class Svc { }
`), "src/Api/Svc.cs")
	ff = append(ff, extractFileAST([]byte(`
namespace Acme.Data;
public class OrderRepository { }
`), "src/Data/OrderRepository.cs")...)
	resolveCSharpTargets(ff)

	var dep *facts.Fact
	for i := range ff {
		if ff[i].Kind == facts.KindDependency && ff[i].Props["import"] == "Acme.Data.OrderRepository" {
			dep = &ff[i]
		}
	}
	if dep == nil {
		t.Fatal("alias using produced no dependency fact")
	}
	if dep.Props["source"] != "internal" {
		t.Errorf("alias using classified %v, want internal", dep.Props["source"])
	}
	if !hasRel(dep, facts.RelImports, "src/Data") {
		t.Errorf("alias using should import the declaring module; got %v", dep.Relations)
	}
}

func TestClassifyUsing_StdlibAndExternal(t *testing.T) {
	ff := extractFileAST([]byte(`
using System.Text;
using Microsoft.Extensions.Logging;
namespace Acme;
public class C { }
`), "src/C.cs")
	resolveCSharpTargets(ff)

	got := map[string]string{}
	for _, f := range ff {
		if f.Kind == facts.KindDependency {
			got[f.Props["import"].(string)], _ = f.Props["source"].(string)
		}
	}
	if got["System.Text"] != "stdlib" {
		t.Errorf("System.Text = %q, want stdlib", got["System.Text"])
	}
	// Microsoft.Extensions is a NuGet package, not the runtime. Calling the whole
	// Microsoft.* tree stdlib would hide every framework dependency a .NET service
	// has.
	if got["Microsoft.Extensions.Logging"] != "external" {
		t.Errorf("Microsoft.Extensions.Logging = %q, want external", got["Microsoft.Extensions.Logging"])
	}
}

// TestInfiniteLoop_CallsStayN1Candidates pins the difference between the two loop
// depths. A `while (true)` polling loop adds no factor of n — its scaling depth
// stays 0 — but its body still runs many times, so a call inside it must remain in
// calls_in_scaling_loop. Gating that list on the scaling depth instead would drop
// exactly the N+1 the list exists to surface.
func TestInfiniteLoop_CallsStayN1Candidates(t *testing.T) {
	ff := extractFileAST([]byte(`
namespace Acme;
public class Poller
{
    public void Run()
    {
        while (true)
        {
            Query();
        }
    }
    private void Query() { }
}
`), "src/Poller.cs")

	run := factByName(ff, "src.Poller.Run")
	if run == nil {
		t.Fatal("Run missing")
	}
	if got := run.Props["scaling_loop_depth"]; got != 0 {
		t.Errorf("scaling_loop_depth = %v, want 0 (an infinite loop adds no factor of n)", got)
	}
	if got := run.Props["loop_depth"]; got != 1 {
		t.Errorf("loop_depth = %v, want 1", got)
	}
	inScaling, _ := run.Props["calls_in_scaling_loop"].([]string)
	if len(inScaling) == 0 || !strings.Contains(strings.Join(inScaling, ","), "Query") {
		t.Errorf("calls_in_scaling_loop = %v, want the in-loop call", run.Props["calls_in_scaling_loop"])
	}
}

// TestConstantLoop_CallsAreNotN1Candidates is the control: a literal-bounded loop
// runs a fixed number of times, so its calls are excluded.
func TestConstantLoop_CallsAreNotN1Candidates(t *testing.T) {
	ff := extractFileAST([]byte(`
namespace Acme;
public class Fixed
{
    public void Run()
    {
        for (var i = 0; i < 3; i++)
        {
            Query();
        }
    }
    private void Query() { }
}
`), "src/Fixed.cs")

	run := factByName(ff, "src.Fixed.Run")
	if got := run.Props["scaling_loop_depth"]; got != 0 {
		t.Errorf("scaling_loop_depth = %v, want 0", got)
	}
	inScaling, _ := run.Props["calls_in_scaling_loop"].([]string)
	if len(inScaling) != 0 {
		t.Errorf("calls_in_scaling_loop = %v, want empty for a constant-trip loop", inScaling)
	}
	// The call is still recorded as in-loop; only the N+1 subset excludes it.
	if inLoop, _ := run.Props["calls_in_loop"].([]string); len(inLoop) == 0 {
		t.Error("calls_in_loop should still record the call")
	}
}

// memberCallTargets runs the full pipeline and returns one symbol's calls targets.
func memberCallTargets(t *testing.T, src, relFile, symbol string) []string {
	t.Helper()
	ff := extractFileAST([]byte(src), relFile)
	ff = mergePartialTypes(ff)
	resolveCSharpTargets(ff)
	f := factByName(ff, symbol)
	if f == nil {
		t.Fatalf("symbol %q missing", symbol)
	}
	var out []string
	for _, r := range f.Relations {
		if r.Kind == facts.RelCalls {
			out = append(out, r.Target)
		}
	}
	sort.Strings(out)
	return out
}

const memberCallSrc = `
namespace Acme;

public interface IOrderRepository
{
    Order Find(int id);
}

public class OrderService
{
    private readonly IOrderRepository _repo;

    public Order Handle(int id)
    {
        // A call through an interface-typed field: the receiver's static type is
        // not tracked, so this is the case the bare edge exists for.
        var order = _repo.Find(id);
        // Declared by nothing in this repository — must be dropped rather than
        // dangling.
        var s = order.ToString();
        return order;
    }
}
`

// TestMemberCall_OnUntrackedReceiverEmitsBareEdge covers the dominant dead-code
// false positive. A DI-wired .NET application calls almost everything through an
// interface, and with a same-type-only call graph those methods had no inbound
// edge at all — 2,478 of jellyfin's 6,975 methods read as unreferenced.
func TestMemberCall_OnUntrackedReceiverEmitsBareEdge(t *testing.T) {
	got := memberCallTargets(t, memberCallSrc, "src/OrderService.cs", "src.OrderService.Handle")
	if !has(got, "Find") {
		t.Errorf("interface-dispatched call should leave a bare reference; got %v", got)
	}
	// The interface member is what the bare name rescues.
	if factByName(extractFileAST([]byte(memberCallSrc), "src/OrderService.cs"), "src.IOrderRepository.Find") == nil {
		t.Fatal("interface member missing")
	}
}

// TestMemberCall_UndeclaredNameIsDropped is the other half. Every `.ToString()`,
// `.Add()` and `.GetAwaiter()` in a repository would otherwise become an edge to a
// symbol that does not exist.
func TestMemberCall_UndeclaredNameIsDropped(t *testing.T) {
	got := memberCallTargets(t, memberCallSrc, "src/OrderService.cs", "src.OrderService.Handle")
	if has(got, "ToString") {
		t.Errorf("a name no type declares must not become an edge; got %v", got)
	}
}

// TestMemberCall_UniqueNameIsNotBound pins the decision that a uniquely-named
// method is still left BARE. jellyfin declares exactly one `Match` method
// (FileStackRule.Match) while four of its five variable-receiver `.Match(` call
// sites are Regex — so binding on uniqueness would have pointed four call sites
// into the video-stack parser. The dead-code detector matches by short name, so
// binding buys nothing and only risks a wrong edge into impact_analysis.
func TestMemberCall_UniqueNameIsNotBound(t *testing.T) {
	got := memberCallTargets(t, `
namespace Acme;

public class FileStackRule
{
    public bool Match(string s) => true;
}

public class Parser
{
    public void Run(System.Text.RegularExpressions.Regex regex, string input)
    {
        // The ONLY Match method this repo declares is FileStackRule.Match, but
        // this receiver is a Regex.
        var m = regex.Match(input);
    }
}
`, "src/Parser.cs", "src.Parser.Run")

	if has(got, "src.FileStackRule.Match") {
		t.Errorf("a unique name must not be bound to a canonical target; got %v", got)
	}
	if !has(got, "Match") {
		t.Errorf("the bare name should survive so short-name consumers still see it; got %v", got)
	}
}

// TestMemberCall_SameTypeAndStaticStillResolve is the control: the precise edges
// this change must not weaken.
func TestMemberCall_SameTypeAndStaticStillResolve(t *testing.T) {
	got := memberCallTargets(t, `
namespace Acme;

public static class Guard
{
    public static void NotNull(object o) { }
}

public class Svc
{
    public void A() { B(); Guard.NotNull(this); }
    public void B() { }
}
`, "src/Svc.cs", "src.Svc.A")

	if !has(got, "src.Svc.B") {
		t.Errorf("same-type call should still resolve to the canonical target; got %v", got)
	}
	if !has(got, "src.Guard.NotNull") {
		t.Errorf("static call should still resolve to the canonical target; got %v", got)
	}
}
