package csharpextractor

import (
	"sort"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func relTargets(f facts.Fact, kind string) []string {
	var out []string
	for _, r := range f.Relations {
		if r.Kind == kind {
			out = append(out, r.Target)
		}
	}
	sort.Strings(out)
	return out
}

func symbolNamed(t *testing.T, ff []facts.Fact, name string) facts.Fact {
	t.Helper()
	for _, f := range ff {
		if f.Kind == facts.KindSymbol && f.Name == name {
			return f
		}
	}
	t.Fatalf("no symbol %q", name)
	return facts.Fact{}
}

// The MudBlazor shape, reduced from src/MudBlazor/Components/Alert/MudAlert.razor.
// Every handler here is declared in the .razor.cs code-behind, which is exactly
// why they read as dead before this existed.
const mudAlertRazor = `@namespace MudBlazor
@using MudBlazor.Resources
@inherits MudComponentBase
@inject InternalMudLocalizer Localizer

<div @attributes="UserAttributes" class="@Classname" Style="@Style" @onclick="this.AsNonRenderingEventHandler<MouseEventArgs>(OnClickHandler)">
<div class="@ClassPosition">
    @if (!NoIcon)
    {
        <div class="mud-alert-icon mud-alert-icon-left">
            <MudIcon Icon="@_icon" />
        </div>
    }
    <div class="mud-alert-message">
        @ChildContent
    </div>
</div>
@if (ShowCloseIcon)
{
    <div class="mud-alert-close">
        <MudIconButton Class="mud-alert-close-button" Icon="@CloseIcon" @onclick="OnCloseIconClickAsync" Size="Size.Small" />
    </div>
}
</div>
`

func TestRazor_ComponentReferencesCodeBehindMembers(t *testing.T) {
	ff := razorFacts(mudAlertRazor, "src/MudBlazor/Components/Alert/MudAlert.razor")
	c := symbolNamed(t, ff, "src/MudBlazor/Components/Alert.MudAlert")

	if c.Props["partial"] != true {
		t.Error("component must be partial so it merges with the .razor.cs half")
	}
	if c.Props["namespace"] != "MudBlazor" {
		t.Errorf("namespace = %v", c.Props["namespace"])
	}

	calls := relTargets(c, facts.RelCalls)
	for _, want := range []string{
		"OnClickHandler",                      // inside a generic call in an @onclick value
		"OnCloseIconClickAsync",               // a bare @onclick handler
		"Classname", "ClassPosition", "Style", // @-expressions in attribute values
		"NoIcon", "ShowCloseIcon", // @if conditions
		"ChildContent", "CloseIcon", "UserAttributes",
	} {
		if !has(calls, want) {
			t.Errorf("missing call reference %q; got %v", want, calls)
		}
	}

	if got := relTargets(c, facts.RelImplements); len(got) != 1 || got[0] != "MudComponentBase" {
		t.Errorf("implements = %v, want [MudComponentBase]", got)
	}
	if got := relTargets(c, facts.RelInjects); len(got) != 1 || got[0] != "InternalMudLocalizer" {
		t.Errorf("injects = %v, want [InternalMudLocalizer]", got)
	}
	inst := relTargets(c, facts.RelInstantiates)
	if !has(inst, "MudIcon") || !has(inst, "MudIconButton") {
		t.Errorf("instantiates = %v, want MudIcon and MudIconButton", inst)
	}
}

// The @onclick attribute NAME must not become a reference — only its value is C#.
func TestRazor_DirectiveAttributeNameIsNotAReference(t *testing.T) {
	ff := razorFacts(mudAlertRazor, "src/MudBlazor/Components/Alert/MudAlert.razor")
	calls := relTargets(symbolNamed(t, ff, "src/MudBlazor/Components/Alert.MudAlert"), facts.RelCalls)
	for _, bad := range []string{"onclick", "attributes", "bind", "code"} {
		if has(calls, bad) {
			t.Errorf("%q is Razor syntax, not a symbol reference; got %v", bad, calls)
		}
	}
}

// The OrchardCore shape. These members are reached ONLY through asp-for, with no
// @ transition at all — a scanner following @ transitions finds none of them.
const adminSettingsCshtml = `@model OrchardCore.Admin.ViewModels.AdminSettingsViewModel

<div class="ocat-wrapper" asp-validation-class-for="DisplayThemeToggler">
    <input asp-for="DisplayThemeToggler" class="form-check-input" type="checkbox" />
    <label asp-for="DisplayMenuFilter">@T["Enable Admin Menu filter"]</label>
    <input asp-for="DisplayNewMenu" type="checkbox" />
    <input asp-for="DisplayTitlesInTopbar" type="checkbox" />
</div>
`

func TestRazor_ViewBindsModelMembersViaTagHelpers(t *testing.T) {
	ff := razorFacts(adminSettingsCshtml,
		"src/OrchardCore.Modules/OrchardCore.Admin/Views/AdminSettings.Edit.cshtml")

	var ref *facts.Fact
	for i := range ff {
		if ff[i].Kind == facts.KindFileRef {
			ref = &ff[i]
		}
	}
	if ref == nil {
		t.Fatal("a .cshtml view must emit a KindFileRef, never a symbol")
	}
	for _, f := range ff {
		if f.Kind == facts.KindSymbol {
			t.Errorf("a view generates a class nothing references by name; got symbol %q", f.Name)
		}
	}

	calls := relTargets(*ref, facts.RelCalls)
	for _, want := range []string{
		"DisplayThemeToggler", "DisplayMenuFilter", "DisplayNewMenu", "DisplayTitlesInTopbar",
		"AdminSettingsViewModel",
	} {
		if !has(calls, want) {
			t.Errorf("missing %q; got %v", want, calls)
		}
	}
}

// An @code block is real C#, and its members must land on the same symbol the
// markup references — at their true line numbers, not offsets into a synthetic
// buffer.
func TestRazor_CodeBlockMembersAndLineNumbers(t *testing.T) {
	src := `<MudToolBar>
    @if (Previous != null)
    {
        <MudButton Href="@($"{Section.ToStringFast(true)}/{Previous.Link}")">@Previous.Name</MudButton>
    }
</MudToolBar>

@code{

    [Parameter]
    public NavigationSection Section { get; set; }

    [Parameter]
    public NavigationFooterLink Previous { get; set; }
}
`
	ff := razorFacts(src, "src/MudBlazor.Docs/Components/NavigationFooter.razor")

	sec := symbolNamed(t, ff, "src/MudBlazor.Docs/Components.NavigationFooter.Section")
	// Line 10 is the `[Parameter]` attribute, line 11 the declaration. The C#
	// walker reports an attributed member at its ATTRIBUTE line (verified against
	// plain C#: a member attributed on line 4 and declared on line 5 reports 4), so
	// 10 is the consistent answer — and it is only the right answer if the
	// synthetic wrapper put the body back on its original line.
	if sec.Line != 10 {
		t.Errorf("Section.Line = %d, want 10 — the wrapper must preserve line numbers", sec.Line)
	}
	symbolNamed(t, ff, "src/MudBlazor.Docs/Components.NavigationFooter.Previous")

	c := symbolNamed(t, ff, "src/MudBlazor.Docs/Components.NavigationFooter")
	calls := relTargets(c, facts.RelCalls)
	if !has(calls, "Previous") || !has(calls, "Section") {
		t.Errorf("markup must reference the @code members; got %v", calls)
	}
}

// A @page route is a URL the browser navigates to, not an HTTP contract. Typing
// it as a server route would make it an "unused route no client calls" finding.
func TestRazor_PageRouteIsAUIRoute(t *testing.T) {
	ff := razorFacts("@page \"/counter/{id:int}\"\n@namespace App.Pages\n<h1>Hi</h1>\n",
		"src/App/Pages/Counter.razor")
	var route *facts.Fact
	for i := range ff {
		if ff[i].Kind == facts.KindRoute {
			route = &ff[i]
		}
	}
	if route == nil {
		t.Fatal("no route emitted for @page")
	}
	if route.Name != "/counter/{id:int}" {
		t.Errorf("route = %q", route.Name)
	}
	if route.Props["type"] != "page" {
		t.Errorf("type = %v, want page (the linker excludes UI routes by this)", route.Props["type"])
	}
	if got := relTargets(*route, facts.RelHandledBy); len(got) != 1 || got[0] != "src/App/Pages.Counter" {
		t.Errorf("handled_by = %v", got)
	}
}

// Comments carry code-shaped text that is not code.
func TestRazor_CommentsAreNotReferences(t *testing.T) {
	ff := razorFacts(`@namespace App
@* @OldHandler was removed *@
<!-- <ObsoleteComponent /> -->
<div>@RealMember</div>
`, "src/App/Thing.razor")
	c := symbolNamed(t, ff, "src/App.Thing")
	calls := relTargets(c, facts.RelCalls)
	if has(calls, "OldHandler") {
		t.Error("a razor comment is not a reference")
	}
	if has(relTargets(c, facts.RelInstantiates), "ObsoleteComponent") {
		t.Error("an HTML comment is not an instantiation")
	}
	if !has(calls, "RealMember") {
		t.Errorf("real reference lost; got %v", calls)
	}
}

// `@@` is an escaped at-sign in markup (email addresses, CSS), not a transition.
func TestRazor_EscapedAtSignIsNotATransition(t *testing.T) {
	ff := razorFacts("@namespace App\n<p>mail@@Example.com</p>\n", "src/App/Thing.razor")
	c := symbolNamed(t, ff, "src/App.Thing")
	if has(relTargets(c, facts.RelCalls), "Example") {
		t.Error("@@ is an escaped literal, not a reference")
	}
}

// A brace inside a string must not close an @code block early.
func TestRazor_BraceInStringDoesNotCloseCodeBlock(t *testing.T) {
	ff := razorFacts(`@namespace App
@code {
    private string Tpl = "}";
    public void AfterTheBrace() { }
}
`, "src/App/Thing.razor")
	symbolNamed(t, ff, "src/App.Thing.AfterTheBrace")
}
