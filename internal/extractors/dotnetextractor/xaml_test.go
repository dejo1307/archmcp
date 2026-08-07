package dotnetextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func fileRefOf(t *testing.T, ff []facts.Fact) facts.Fact {
	t.Helper()
	for _, f := range ff {
		if f.Kind == facts.KindFileRef {
			return f
		}
	}
	t.Fatalf("no file_ref fact")
	return facts.Fact{}
}

// The WinUI shape, reduced from files-community/Files.
const filesSplashXaml = `<Page
	x:Class="Files.App.Views.SplashScreenPage"
	xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
	xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml"
	xmlns:conv="using:Files.App.Converters"
	Background="{ThemeResource ApplicationPageBackgroundThemeBrush}">
	<Grid>
		<Image x:Name="SplashScreenImage"
			ImageFailed="Image_ImageFailed"
			ImageOpened="Image_ImageOpened" />
		<TextBlock Text="{x:Bind BranchLabel, Mode=OneTime}" />
		<Button Content="Go" Click="OnGoClicked"
			IsEnabled="{Binding Path=CanNavigate, Converter={StaticResource BoolNegationConverter}}" />
		<conv:StatusCenterItem />
	</Grid>
</Page>`

func TestXaml_ViewMergesWithCodeBehind(t *testing.T) {
	ff := xamlFacts(filesSplashXaml, "src/Files.App/Views/SplashScreenPage.xaml")
	v := symbolNamed(t, ff, "src/Files.App/Views.SplashScreenPage")

	if v.Props["partial"] != true {
		t.Error("must be partial so it merges with SplashScreenPage.xaml.cs")
	}
	if v.Props["symbol_kind"] != facts.SymbolClass {
		t.Errorf("symbol_kind = %v, want class (it must enter the type index)", v.Props["symbol_kind"])
	}
	if v.Props["fqn"] != "Files.App.Views.SplashScreenPage" {
		t.Errorf("fqn = %v", v.Props["fqn"])
	}
	if v.Props["namespace"] != "Files.App.Views" {
		t.Errorf("namespace = %v", v.Props["namespace"])
	}
}

func TestXaml_EventHandlersAndBindingsBecomeCalls(t *testing.T) {
	ff := xamlFacts(filesSplashXaml, "src/Files.App/Views/SplashScreenPage.xaml")
	calls := relTargets(symbolNamed(t, ff, "src/Files.App/Views.SplashScreenPage"), facts.RelCalls)

	for _, want := range []string{
		"Image_ImageFailed", "Image_ImageOpened", // underscore convention
		"OnGoClicked",           // On* convention on a known event
		"BranchLabel",           // {x:Bind}
		"CanNavigate",           // {Binding Path=}
		"BoolNegationConverter", // a nested {StaticResource}
	} {
		if !has(calls, want) {
			t.Errorf("missing %q; got %v", want, calls)
		}
	}
}

// A tag in a prefix bound to a CLR namespace is a repository type; framework
// controls in the default schema namespace are not.
func TestXaml_OnlyCLRPrefixedTagsAreInstantiations(t *testing.T) {
	ff := xamlFacts(filesSplashXaml, "src/Files.App/Views/SplashScreenPage.xaml")
	inst := relTargets(symbolNamed(t, ff, "src/Files.App/Views.SplashScreenPage"), facts.RelInstantiates)
	if !has(inst, "StatusCenterItem") {
		t.Errorf("conv: is bound to a clr namespace; got %v", inst)
	}
	for _, bad := range []string{"Grid", "Image", "TextBlock", "Button", "Page"} {
		if has(inst, bad) {
			t.Errorf("%q is a framework control, not a repository type; got %v", bad, inst)
		}
	}
}

// Enum-valued attributes are the reason handler detection is gated rather than
// treating every bare identifier as a method name.
func TestXaml_EnumValuesAreNotReferences(t *testing.T) {
	ff := xamlFacts(`<Page x:Class="A.B" xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml">
		<Image Stretch="None" />
		<TextBlock FontWeight="SemiBold" HorizontalAlignment="Center" />
	</Page>`, "src/A/B.xaml")
	calls := relTargets(symbolNamed(t, ff, "src/A.B"), facts.RelCalls)
	for _, bad := range []string{"None", "SemiBold", "Center"} {
		if has(calls, bad) {
			t.Errorf("%q is an enum value, not a member reference; got %v", bad, calls)
		}
	}
}

// Binding configuration is XAML syntax, not code.
func TestXaml_BindingConfigurationIsNotAReference(t *testing.T) {
	ff := xamlFacts(`<Page x:Class="A.B" xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml">
		<TextBlock Text="{Binding Title, Mode=TwoWay, FallbackValue=Loading, RelativeSource={RelativeSource AncestorType=Page}}" />
	</Page>`, "src/A/B.xaml")
	calls := relTargets(symbolNamed(t, ff, "src/A.B"), facts.RelCalls)
	if !has(calls, "Title") {
		t.Errorf("the bound path is lost; got %v", calls)
	}
	for _, bad := range []string{"TwoWay", "Loading", "Binding", "RelativeSource", "Page"} {
		if has(calls, bad) {
			t.Errorf("%q configures the binding, it does not name code; got %v", bad, calls)
		}
	}
}

// Avalonia uses its own default namespace and the .axaml extension.
func TestXaml_AvaloniaDialect(t *testing.T) {
	ff := xamlFacts(`<UserControl xmlns="https://github.com/avaloniaui"
		xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml"
		xmlns:vm="clr-namespace:App.ViewModels"
		x:Class="WinUIEmbedSample.EmbeddedView">
		<Button x:Name="AvButton" Click="OnAvButtonClick" />
		<ContentControl Content="{CompiledBinding SelectedItem}" />
		<vm:MainWindowViewModel />
	</UserControl>`, "samples/WinUIEmbedSample/EmbeddedView.axaml")

	v := symbolNamed(t, ff, "samples/WinUIEmbedSample.EmbeddedView")
	if v.Props["framework"] != "avalonia" {
		t.Errorf("framework = %v, want avalonia", v.Props["framework"])
	}
	calls := relTargets(v, facts.RelCalls)
	if !has(calls, "OnAvButtonClick") || !has(calls, "SelectedItem") {
		t.Errorf("handler/binding lost; got %v", calls)
	}
	if !has(relTargets(v, facts.RelInstantiates), "MainWindowViewModel") {
		t.Error("a clr-namespace tag is an instantiation")
	}
}

// x:Name declares a code-behind field; it is not a reference to one.
func TestXaml_NameAndKeyAreDeclarationsNotReferences(t *testing.T) {
	ff := xamlFacts(`<Page x:Class="A.B" xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml">
		<Grid x:Name="RootGrid" />
		<SolidColorBrush x:Key="AccentBrush" />
	</Page>`, "src/A/B.xaml")
	calls := relTargets(symbolNamed(t, ff, "src/A.B"), facts.RelCalls)
	for _, bad := range []string{"RootGrid", "AccentBrush"} {
		if has(calls, bad) {
			t.Errorf("%q is declared here, not referenced; got %v", bad, calls)
		}
	}
}

// A ResourceDictionary or style file has no x:Class, so there is no class to
// attach to and it must not invent one.
func TestXaml_NoClassEmitsFileRefNotSymbol(t *testing.T) {
	ff := xamlFacts(`<ResourceDictionary xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
		xmlns:conv="clr-namespace:App.Converters">
		<conv:BoolToVisibilityConverter x:Key="B2V" />
	</ResourceDictionary>`, "src/App/Styles/Theme.xaml")

	for _, f := range ff {
		if f.Kind == facts.KindSymbol {
			t.Errorf("no x:Class means no class; got symbol %q", f.Name)
		}
	}
	if !has(relTargets(fileRefOf(t, ff), facts.RelCalls), "BoolToVisibilityConverter") {
		t.Error("the converter reference is what keeps it from reading as dead")
	}
}

// A malformed document must degrade to what was read, not crash the walk.
func TestXaml_MalformedDocumentDegrades(t *testing.T) {
	ff := xamlFacts(`<Page x:Class="A.B" xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml">
		<Button Click="OnGo" />
		<Unclosed>`, "src/A/B.xaml")
	if !has(relTargets(symbolNamed(t, ff, "src/A.B"), facts.RelCalls), "OnGo") {
		t.Error("what was read before the break should survive")
	}
}
