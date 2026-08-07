package dotnetextractor

// XAML — WPF, WinUI/UWP, MAUI and Avalonia views.
//
// Unlike Razor, XAML *is* XML, so this is a real token walk rather than a regex
// scan. What it looks for is the three ways a view reaches into code:
//
//	x:Class="Ns.MainWindow"      the code-behind half of a partial class
//	Click="OnSave"               an event handler method
//	{Binding Path=Title}         a member on the bound view model
//
// The measured problem, before this existed: files-community/Files reported
// 4,819 orphans, 15.9% of them referenced only from XAML — dependency properties
// like `AdaptiveGridView.ItemsPanel` and `BladeItem.CloseButtonForeground` whose
// sole use is a binding. Avalonia's converters and views went the same way.
//
// As with Razor, the view and its code-behind converge through the ordinary
// partial-type merge: both name the same directory-anchored symbol and both carry
// `partial`.

import (
	"encoding/xml"
	"io"
	"path"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

func isXamlFile(relFile string) bool {
	switch strings.ToLower(filepath.Ext(relFile)) {
	case ".xaml", ".axaml":
		return true
	}
	return false
}

// xamlDoc is what one XAML file declares and references.
type xamlDoc struct {
	class     string   // x:Class, fully qualified
	framework string   // avalonia | maui | xaml
	controls  []string // custom control types instantiated by tag
	refs      []string // handlers, bound members, converters, resource keys
}

// xamlEvents are the attribute names whose value is a code-behind METHOD rather
// than a value. The list is deliberately short and the convention check below
// carries the rest: a bare-identifier attribute value is far more often an enum
// member (`Stretch="None"`, `FontWeight="SemiBold"`) than a handler, and treating
// those as references would vouch for symbols nothing uses — which suppresses
// genuine dead-code findings, the direction worth guarding.
var xamlEvents = map[string]bool{
	"Click": true, "Tapped": true, "DoubleTapped": true, "Loaded": true,
	"Unloaded": true, "Checked": true, "Unchecked": true, "Opened": true,
	"Closed": true, "Closing": true, "SelectionChanged": true, "TextChanged": true,
	"ValueChanged": true, "PointerPressed": true, "PointerReleased": true,
	"PointerMoved": true, "PointerEntered": true, "PointerExited": true,
	"KeyDown": true, "KeyUp": true, "GotFocus": true, "LostFocus": true,
	"DragOver": true, "Drop": true, "DragEnter": true, "DragLeave": true,
	"ItemInvoked": true, "ItemClick": true, "Expanding": true, "Collapsed": true,
	"Navigated": true, "Initialized": true, "AttachedToVisualTree": true,
	"DetachedFromVisualTree": true, "PropertyChanged": true, "SizeChanged": true,
	"ImageOpened": true, "ImageFailed": true, "Completed": true, "Toggled": true,
	"RightTapped": true, "Holding": true, "ContextRequested": true,
}

// looksLikeHandler reports whether a bare attribute value follows one of the two
// dominant handler-naming conventions. It is a convention check, not a proof, and
// exists so an event this file's list does not know still resolves.
func looksLikeHandler(v string) bool {
	if strings.Contains(v, "_") {
		return true // Image_ImageFailed, Button_Click — the designer-generated form
	}
	return strings.HasPrefix(v, "On") && len(v) > 2 && v[2] >= 'A' && v[2] <= 'Z'
}

// scanXaml walks a XAML document.
func scanXaml(src, relFile string) *xamlDoc {
	d := &xamlDoc{framework: "xaml"}
	// Keyed by NAMESPACE URI, not by prefix: encoding/xml resolves a prefix to its
	// URI before reporting a name, so `<conv:StatusCenterItem>` arrives with
	// Space="using:Files.App.Converters". A prefix-keyed map never matches.
	//
	// A clr-namespace/using URI names types declared in this solution; the
	// schema-URL ones (presentation, mc, d) name framework built-ins.
	clrNS := map[string]bool{}

	dec := xml.NewDecoder(strings.NewReader(src))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	dec.Strict = false // XAML in the wild carries undeclared entities

	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		for _, a := range se.Attr {
			local, prefix := a.Name.Local, a.Name.Space

			// Namespace declarations. encoding/xml reports xmlns:local as
			// Space="xmlns", Local="local".
			if prefix == "xmlns" || local == "xmlns" {
				if isCLRNamespace(a.Value) {
					clrNS[a.Value] = true
				}
				switch {
				case strings.Contains(a.Value, "github.com/avaloniaui"):
					d.framework = "avalonia"
				case strings.Contains(a.Value, "dotnet/2021/maui"):
					d.framework = "maui"
				}
				continue
			}

			if local == "Class" && strings.Contains(prefix, "winfx/2006/xaml") {
				d.class = a.Value
				continue
			}
			// x:Name declares a field on the code-behind; it is a declaration, not a
			// reference, and the code-behind's own use of it is already an edge.
			if local == "Name" || local == "Key" || local == "Uid" {
				continue
			}

			switch {
			case strings.HasPrefix(strings.TrimSpace(a.Value), "{"):
				harvestMarkupExtension(d, a.Value)
			case xamlEvents[local] || looksLikeHandler(a.Value):
				if isBareIdentifier(a.Value) {
					d.refs = append(d.refs, a.Value)
				}
			}
		}

		// A tag in a CLR-mapped prefix is a type declared in this solution.
		// Framework controls (Grid, TextBlock) live in the default schema
		// namespace and are not repository symbols.
		if se.Name.Space != "" && clrNS[se.Name.Space] {
			d.controls = append(d.controls, se.Name.Local)
		}
	}

	d.controls = dedupeSorted(d.controls)
	d.refs = dedupeSorted(d.refs)
	return d
}

func isCLRNamespace(v string) bool {
	return strings.HasPrefix(v, "clr-namespace:") || strings.HasPrefix(v, "using:")
}

func isBareIdentifier(v string) bool {
	if v == "" || !isIdentStart(v[0]) {
		return false
	}
	for i := 0; i < len(v); i++ {
		if !isIdentPart(v[i]) {
			return false
		}
	}
	return !csharpNoise[v]
}

// harvestMarkupExtension reads `{Binding Path=A.B}`, `{x:Bind Vm.Title}`,
// `{StaticResource BoolToVisibility}` and their nested forms.
//
// Only the extension's ARGUMENTS are harvested, never its name: `Binding`,
// `StaticResource` and `x:Bind` are XAML syntax. Named arguments that configure
// the binding rather than name code (Mode, RelativeSource, ElementName,
// FallbackValue, …) are dropped for the same reason.
func harvestMarkupExtension(d *xamlDoc, v string) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "{") {
		return
	}
	depth := 0
	start := 0
	var flush func(seg string)
	flush = func(seg string) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return
		}
		if strings.HasPrefix(seg, "{") {
			harvestMarkupExtension(d, seg)
			return
		}
		if i := strings.IndexByte(seg, '='); i >= 0 {
			key := strings.TrimSpace(seg[:i])
			val := strings.TrimSpace(seg[i+1:])
			if bindingNoise[key] {
				return
			}
			flush(val)
			return
		}
		// A positional argument: the extension name for the first one, a path or a
		// resource key afterwards.
		for _, part := range strings.Split(seg, ".") {
			part = strings.TrimSpace(part)
			// Strip an indexer or attached-property parenthesis.
			part = strings.TrimSuffix(part, ")")
			part = strings.TrimPrefix(part, "(")
			if i := strings.IndexByte(part, '['); i >= 0 {
				part = part[:i]
			}
			if isBareIdentifier(part) && !xamlExtensionNames[part] {
				d.refs = append(d.refs, part)
			}
		}
	}

	body := strings.TrimSuffix(strings.TrimPrefix(v, "{"), "}")
	// The extension name runs to the first space; skip it, keep the arguments.
	if i := strings.IndexAny(body, " \t"); i >= 0 {
		body = body[i+1:]
	} else {
		return // `{Binding}` on its own names nothing
	}

	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				flush(body[start:i])
				start = i + 1
			}
		}
	}
	flush(body[start:])
}

// xamlExtensionNames are markup-extension names, which are syntax rather than
// repository symbols even when they appear in an argument position.
var xamlExtensionNames = map[string]bool{
	"Binding": true, "Bind": true, "StaticResource": true, "DynamicResource": true,
	"ThemeResource": true, "TemplateBinding": true, "RelativeSource": true,
	"CompiledBinding": true, "Type": true, "Static": true, "Null": true,
	"OnPlatform": true, "OnIdiom": true, "AppThemeBinding": true, "Reflect": true,
}

// bindingNoise are named arguments that configure a binding rather than name code.
var bindingNoise = map[string]bool{
	"Mode": true, "ElementName": true, "RelativeSource": true, "FallbackValue": true,
	"TargetNullValue": true, "StringFormat": true, "UpdateSourceTrigger": true,
	"Source": true, "AncestorType": true, "AncestorLevel": true, "Delay": true,
	"ConverterParameter": true, "ConverterLanguage": true, "x:DataType": true,
	"DataType": true, "Priority": true, "TargetType": true,
}

// ── Fact emission ───────────────────────────────────────────────────────────

// xamlFacts turns one XAML document into facts.
//
// With an x:Class the document is one half of a partial class, exactly like a
// .razor component, and emits a symbol that merges with the code-behind. Without
// one — a ResourceDictionary, a style file, an App.xaml — there is no class to
// attach to, so it emits a reference-only file_ref.
func xamlFacts(src, relFile string) []facts.Fact {
	d := scanXaml(src, relFile)
	rel := filepath.ToSlash(relFile)
	dir := path.Dir(rel)

	targets := make([]string, 0, len(d.refs)+len(d.controls))
	targets = append(targets, d.refs...)
	targets = append(targets, d.controls...)

	if d.class == "" {
		if len(targets) == 0 {
			return nil
		}
		rels := make([]facts.Relation, 0, len(targets))
		for _, t := range dedupeSorted(targets) {
			rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: t})
		}
		return []facts.Fact{{
			Kind:      facts.KindFileRef,
			Name:      rel,
			File:      rel,
			Props:     map[string]any{"language": "xaml", "framework": d.framework},
			Relations: rels,
		}}
	}

	name := shortType(d.class)
	props := map[string]any{
		"language": "xaml",
		// A class, for the same reason a Razor component is one: symbol_kind is what
		// puts a name into the type index that resolves bare type references, and
		// this fact merges over the code-behind half.
		"symbol_kind": facts.SymbolClass,
		"xaml_view":   true,
		"framework":   d.framework,
		"exported":    true,
		"partial":     true,
		"fqn":         d.class,
	}
	if i := strings.LastIndex(d.class, "."); i > 0 {
		props["namespace"] = d.class[:i]
	}

	rels := []facts.Relation{{Kind: facts.RelDeclares, Target: dir}}
	for _, c := range d.controls {
		if c != name {
			rels = append(rels, facts.Relation{Kind: facts.RelInstantiates, Target: c})
		}
	}
	for _, r := range d.refs {
		rels = append(rels, facts.Relation{Kind: facts.RelCalls, Target: r})
	}
	return []facts.Fact{{
		Kind:      facts.KindSymbol,
		Name:      dir + "." + name,
		File:      rel,
		Line:      1,
		Props:     props,
		Relations: rels,
	}}
}
