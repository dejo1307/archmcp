package dotnetextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// Reduced from dotnet/roslyn's VB compiler, which is the corpus this exists for.
const roslynVB = `' Licensed to the .NET Foundation under one or more agreements.

Imports System.Collections.Immutable
Imports TypeKind = Microsoft.CodeAnalysis.TypeKind

Namespace Microsoft.CodeAnalysis.VisualBasic.Symbols

    ''' <summary>Represents a type declared in source.</summary>
    Partial Friend Class SourceNamedTypeSymbol
        Inherits SourceMemberContainerTypeSymbol
        Implements IAttributeTargetSymbol

        Private _lazyDocComment As String
        Public Const DelegateFlags As SourceMemberFlags = SourceMemberFlags.Overridable

        Friend Sub New(declaration As MergedTypeDeclaration,
                       containingSymbol As NamespaceOrTypeSymbol)
            MyBase.New(declaration)
            _lazyDocComment = GetDocComment(declaration)
        End Sub

        Public Overrides ReadOnly Property ExtendedSpecialType As ExtendedSpecialType
            Get
                Return _corTypeId
            End Get
        End Property

        Friend Function GetTypeIdentifierToken(node As VisualBasicSyntaxNode) As SyntaxToken
            For Each part In declaration.Declarations
                If part.IsMerged Then
                    Return SyntaxFacts.GetIdentifier(part)
                End If
            Next
            Return Nothing
        End Function
    End Class
End Namespace
`

func TestVB_TypesAndMembers(t *testing.T) {
	ff := scanVB(roslynVB, "src/Compilers/VisualBasic/Portable/Symbols/SourceNamedTypeSymbol.vb")
	dir := "src/Compilers/VisualBasic/Portable/Symbols"

	ty := symbolNamed(t, ff, dir+".SourceNamedTypeSymbol")
	if ty.Props["symbol_kind"] != facts.SymbolClass {
		t.Errorf("symbol_kind = %v", ty.Props["symbol_kind"])
	}
	if ty.Props["partial"] != true {
		t.Error("Partial not read")
	}
	if ty.Props["namespace"] != "Microsoft.CodeAnalysis.VisualBasic.Symbols" {
		t.Errorf("namespace = %v", ty.Props["namespace"])
	}
	// `Friend` is VB's internal, so it is not part of the exported surface.
	if ty.Props["exported"] != false {
		t.Errorf("exported = %v, want false for Friend", ty.Props["exported"])
	}
	impl := relTargets(ty, facts.RelImplements)
	if !has(impl, "SourceMemberContainerTypeSymbol") || !has(impl, "IAttributeTargetSymbol") {
		t.Errorf("Inherits/Implements = %v", impl)
	}

	symbolNamed(t, ff, dir+".SourceNamedTypeSymbol.GetTypeIdentifierToken")
	symbolNamed(t, ff, dir+".SourceNamedTypeSymbol.ExtendedSpecialType")
	symbolNamed(t, ff, dir+".SourceNamedTypeSymbol.DelegateFlags") // Public Const
}

// A multi-line parameter list is the dominant declaration shape in roslyn's VB;
// without continuation folding the second line parses as a stray statement and
// the constructor is lost.
func TestVB_MultiLineDeclarationIsOneStatement(t *testing.T) {
	ff := scanVB(roslynVB, "src/x/S.vb")
	c := symbolNamed(t, ff, "src/x.SourceNamedTypeSymbol.New")
	if c.Props["symbol_kind"] != "constructor" {
		t.Errorf("symbol_kind = %v, want constructor", c.Props["symbol_kind"])
	}
}

// Private state is not a node anyone traverses — the same rule the C# walker
// applies to private fields.
func TestVB_PrivateFieldsAreNotSymbols(t *testing.T) {
	ff := scanVB(roslynVB, "src/x/S.vb")
	for _, f := range ff {
		if f.Kind == facts.KindSymbol && f.Name == "src/x.SourceNamedTypeSymbol._lazyDocComment" {
			t.Error("a Private field must not become a symbol")
		}
	}
}

func TestVB_ImportsBecomeDependencies(t *testing.T) {
	ff := scanVB(roslynVB, "src/x/S.vb")
	var plain, aliased bool
	for _, f := range ff {
		if f.Kind != facts.KindDependency {
			continue
		}
		if f.Props["import"] == "System.Collections.Immutable" {
			plain = true
		}
		if f.Props["import"] == "Microsoft.CodeAnalysis.TypeKind" && f.Props["alias"] == "TypeKind" {
			aliased = true
		}
	}
	if !plain {
		t.Error("plain Imports lost")
	}
	if !aliased {
		t.Error("aliased Imports lost")
	}
}

// The whole point of the phase: a VB member's outbound references, which are what
// stop the C# symbols it calls from reading as dead.
func TestVB_BodyReferences(t *testing.T) {
	ff := scanVB(roslynVB, "src/x/S.vb")
	calls := relTargets(symbolNamed(t, ff, "src/x.SourceNamedTypeSymbol.GetTypeIdentifierToken"), facts.RelCalls)
	for _, want := range []string{"SyntaxFacts", "GetIdentifier", "Declarations", "IsMerged"} {
		if !has(calls, want) {
			t.Errorf("missing reference %q; got %v", want, calls)
		}
	}
}

// VB is case-insensitive; lowercase keywords are legal and must parse the same.
func TestVB_CaseInsensitiveKeywords(t *testing.T) {
	ff := scanVB(`namespace Acme
    public class Widget
        public sub Run()
            Helper.Go()
        end sub
    end class
end namespace
`, "src/x/W.vb")
	w := symbolNamed(t, ff, "src/x.Widget")
	if w.Props["exported"] != true {
		t.Errorf("lowercase `public` not recognised: %v", w.Props)
	}
	calls := relTargets(symbolNamed(t, ff, "src/x.Widget.Run"), facts.RelCalls)
	if !has(calls, "Helper") || !has(calls, "Go") {
		t.Errorf("calls = %v", calls)
	}
}

// A `Module` is VB's static class. It must be a class in the type index or bare
// references to it stop resolving.
func TestVB_ModuleIsAClass(t *testing.T) {
	ff := scanVB("Namespace N\n  Public Module LanguageVersionFacts\n    Public Function TryParse() As Boolean\n    End Function\n  End Module\nEnd Namespace\n", "src/x/L.vb")
	m := symbolNamed(t, ff, "src/x.LanguageVersionFacts")
	if m.Props["symbol_kind"] != facts.SymbolClass {
		t.Errorf("symbol_kind = %v, want class", m.Props["symbol_kind"])
	}
	if m.Props["vb_module"] != true {
		t.Error("vb_module flag lost")
	}
	symbolNamed(t, ff, "src/x.LanguageVersionFacts.TryParse")
}

// `Handles` is how a WinForms/WPF handler is wired. Without it the handler has no
// inbound edge and reads as dead — the VB counterpart of XAML's Click=.
func TestVB_HandlesClauseIsAReference(t *testing.T) {
	ff := scanVB(`Public Class Form1
    Private Sub SaveButton_Click(sender As Object, e As EventArgs) Handles SaveButton.Click
    End Sub
End Class
`, "src/x/F.vb")
	calls := relTargets(symbolNamed(t, ff, "src/x.Form1.SaveButton_Click"), facts.RelCalls)
	if !has(calls, "Click") {
		t.Errorf("Handles target lost; got %v", calls)
	}
}

// A member implementing an interface member names it explicitly in VB.
func TestVB_MemberImplementsClause(t *testing.T) {
	ff := scanVB(`Public Class G
    Public Overloads Function Equals(other As G) As Boolean Implements IEquatable(Of G).Equals
    End Function
End Class
`, "src/x/G.vb")
	impl := relTargets(symbolNamed(t, ff, "src/x.G.Equals"), facts.RelImplements)
	if !has(impl, "IEquatable") {
		t.Errorf("implements = %v, want IEquatable (generic args dropped)", impl)
	}
}

// A comment marker inside a string is not a comment.
func TestVB_ApostropheInStringIsNotAComment(t *testing.T) {
	ff := scanVB(`Public Class C
    Public Sub Run()
        Log.Write("it's fine")
        Helper.After()
    End Sub
End Class
`, "src/x/C.vb")
	calls := relTargets(symbolNamed(t, ff, "src/x.C.Run"), facts.RelCalls)
	if !has(calls, "After") {
		t.Errorf("code after a string containing ' was dropped; got %v", calls)
	}
}

func TestVB_ComplexityMetrics(t *testing.T) {
	ff := scanVB(`Public Class C
    Public Sub Run()
        For Each x In items
            If x.IsValid AndAlso x.Ready Then
                Process(x)
            End If
        Next
    End Sub
End Class
`, "src/x/C.vb")
	m := symbolNamed(t, ff, "src/x.C.Run")
	if c, _ := m.Props["cyclomatic"].(int); c < 4 {
		t.Errorf("cyclomatic = %v, want >= 4 (For + If + AndAlso + base)", m.Props["cyclomatic"])
	}
	if d, _ := m.Props["loop_depth"].(int); d != 1 {
		t.Errorf("loop_depth = %v, want 1", m.Props["loop_depth"])
	}
}

// Generated VB carries the same conventions as generated C# and produces nothing.
func TestVB_GeneratedFilesAreSkipped(t *testing.T) {
	for _, f := range []string{"src/x/Form1.Designer.vb", "src/x/My Project/Settings.vb", "src/x/Syntax.Generated.vb"} {
		if !isGeneratedVB(f, "") {
			t.Errorf("%s should be treated as generated", f)
		}
	}
	if !isGeneratedVB("src/x/Plain.vb", "' <auto-generated>\nNamespace N\nEnd Namespace\n") {
		t.Error("auto-generated header not honoured")
	}
	if isGeneratedVB("src/x/Real.vb", "Namespace N\nEnd Namespace\n") {
		t.Error("a real source file must not be skipped")
	}
}
