package tsextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func setupSvelteProject(t *testing.T, files map[string]string, sveltekit bool) string {
	t.Helper()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkgJSON := `{"dependencies": {"svelte": "^5.0.0"}}`
	if sveltekit {
		pkgJSON = `{"dependencies": {"svelte": "^5.0.0", "@sveltejs/kit": "^2.0.0"}}`
		if err := os.WriteFile(filepath.Join(dir, "svelte.config.js"), []byte(`export default {}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	for relPath, content := range files {
		absPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func extractSvelte(t *testing.T, files map[string]string, sveltekit bool) []facts.Fact {
	t.Helper()
	dir := setupSvelteProject(t, files, sveltekit)

	var relFiles []string
	for f := range files {
		relFiles = append(relFiles, f)
	}

	ext := New()
	result, err := ext.Extract(context.Background(), dir, relFiles)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return result
}

// --- SFC script block extraction ---

func TestExtractSvelteScriptBlocks_Instance(t *testing.T) {
	src := []byte(`<script lang="ts">
  let count = 0
</script>

<button on:click={() => count++}>{count}</button>
`)
	blocks := extractSvelteScriptBlocks(src)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].IsModule {
		t.Error("expected IsModule = false")
	}
	if blocks[0].Lang != "ts" {
		t.Errorf("lang = %q, want ts", blocks[0].Lang)
	}
}

func TestExtractSvelteScriptBlocks_Module(t *testing.T) {
	src := []byte(`<script module>
  export const prerender = true
</script>

<script lang="ts">
  let name = 'world'
</script>

<h1>Hello {name}</h1>
`)
	blocks := extractSvelteScriptBlocks(src)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if !blocks[0].IsModule {
		t.Error("first block should be module")
	}
	if blocks[1].IsModule {
		t.Error("second block should not be module")
	}
}

func TestExtractSvelteScriptBlocks_ContextModule(t *testing.T) {
	src := []byte(`<script context="module">
  export const prerender = true
</script>
`)
	blocks := extractSvelteScriptBlocks(src)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !blocks[0].IsModule {
		t.Error("expected IsModule = true for context=\"module\"")
	}
}

func TestExtractSvelteScriptBlocks_NoScript(t *testing.T) {
	src := []byte(`<h1>Hello</h1>`)
	blocks := extractSvelteScriptBlocks(src)
	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks, got %d", len(blocks))
	}
}

// --- Framework detection ---

func TestDetectSvelte(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"dependencies":{"svelte":"^5.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !detectSvelte(dir) {
		t.Error("expected detectSvelte = true")
	}
}

func TestDetectSvelteKit_Config(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "svelte.config.js"), []byte(`export default {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !detectSvelteKit(dir) {
		t.Error("expected detectSvelteKit = true")
	}
}

func TestDetectSvelteKit_PkgDep(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"devDependencies":{"@sveltejs/kit":"^2.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !detectSvelteKit(dir) {
		t.Error("expected detectSvelteKit = true")
	}
}

// --- SvelteKit route detection ---

func TestDetectSvelteKitRoute(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		wantRoute string
		wantType  string
		wantMethod string
	}{
		{"root page", "src/routes/+page.svelte", "/", "page", "GET"},
		{"nested page", "src/routes/about/+page.svelte", "/about", "page", "GET"},
		{"dynamic param", "src/routes/blog/[slug]/+page.svelte", "/blog/[slug]", "page", "GET"},
		{"layout", "src/routes/+layout.svelte", "/", "layout", "GET"},
		{"api route", "src/routes/api/users/+server.ts", "/api/users", "server", "ALL"},
		{"route group stripped", "src/routes/(app)/dashboard/+page.svelte", "/dashboard", "page", "GET"},
		{"error page", "src/routes/+error.svelte", "/", "error", "GET"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectSvelteKitRoute(tt.file)
			if got == nil {
				t.Fatal("expected route fact, got nil")
			}
			if got.Name != tt.wantRoute {
				t.Errorf("route = %q, want %q", got.Name, tt.wantRoute)
			}
			if got.Props["type"] != tt.wantType {
				t.Errorf("type = %v, want %s", got.Props["type"], tt.wantType)
			}
			if got.Props["method"] != tt.wantMethod {
				t.Errorf("method = %v, want %s", got.Props["method"], tt.wantMethod)
			}
			if got.Props["framework"] != "sveltekit" {
				t.Errorf("framework = %v, want sveltekit", got.Props["framework"])
			}
		})
	}
}

func TestDetectSvelteKitRoute_ServerLoad(t *testing.T) {
	got := detectSvelteKitRoute("src/routes/+page.server.ts")
	if got != nil {
		t.Error("+page.server.ts should not emit a route (it's a load function, not a route)")
	}
}

func TestDetectSvelteKitRoute_NonRoute(t *testing.T) {
	got := detectSvelteKitRoute("src/lib/components/Button.svelte")
	if got != nil {
		t.Error("non-route .svelte file should return nil")
	}
}

// --- Full extraction ---

func TestExtract_SvelteSFC(t *testing.T) {
	ff := extractSvelte(t, map[string]string{
		"src/lib/Counter.svelte": `<script lang="ts">
  let count = $state(0)

  function increment() {
    count++
  }
</script>

<button onclick={increment}>{count}</button>
`,
	}, false)

	comp, ok := findFact(ff, "src/lib.Counter")
	if !ok {
		t.Fatalf("expected component fact src/lib.Counter; got %v", factNames(ff))
	}
	if comp.Props["web_component"] != "component" {
		t.Errorf("web_component = %v, want component", comp.Props["web_component"])
	}
	if comp.Props["framework"] != "svelte" {
		t.Errorf("framework = %v, want svelte", comp.Props["framework"])
	}

	inc, ok := findFact(ff, "src/lib.increment")
	if !ok {
		t.Fatalf("expected fact src/lib.increment; got %v", factNames(ff))
	}
	if inc.Props["symbol_kind"] != facts.SymbolFunc {
		t.Errorf("increment symbol_kind = %v, want function", inc.Props["symbol_kind"])
	}
}

func TestExtract_SvelteSFC_TemplateOnly(t *testing.T) {
	ff := extractSvelte(t, map[string]string{
		"src/lib/Divider.svelte": `<hr/>`,
	}, false)

	comp, ok := findFact(ff, "src/lib.Divider")
	if !ok {
		t.Fatalf("expected component fact src/lib.Divider; got %v", factNames(ff))
	}
	if comp.Props["web_component"] != "component" {
		t.Errorf("web_component = %v, want component", comp.Props["web_component"])
	}
}

func TestExtract_SvelteKitPage(t *testing.T) {
	ff := extractSvelte(t, map[string]string{
		"src/routes/about/+page.svelte": `<script lang="ts">
  const title = 'About'
</script>

<h1>{title}</h1>
`,
	}, true)

	comp, ok := findFact(ff, "src/routes/about.AboutPage")
	if !ok {
		t.Fatalf("expected component fact; got %v", factNames(ff))
	}
	if comp.Props["framework"] != "sveltekit" {
		t.Errorf("framework = %v, want sveltekit", comp.Props["framework"])
	}

	routes := findFactsByKind(ff, facts.KindRoute)
	var found bool
	for _, r := range routes {
		if r.Name == "/about" && r.Props["framework"] == "sveltekit" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected sveltekit route /about; routes: %v", routes)
	}
}

func TestExtract_SvelteKit_LibAlias(t *testing.T) {
	ff := extractSvelte(t, map[string]string{
		"src/routes/+page.svelte": `<script lang="ts">
  import { formatName } from '$lib/utils'
</script>

<h1>{formatName('world')}</h1>
`,
		"src/lib/utils.ts": `export function formatName(s: string) { return s }`,
	}, true)

	deps := findFactsByKind(ff, facts.KindDependency)
	found := false
	for _, d := range deps {
		for _, r := range d.Relations {
			if r.Target == "src/lib/utils" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected $lib/utils to resolve to src/lib/utils")
	}
}

func TestIsSvelteFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"src/App.svelte", true},
		{"src/app.SVELTE", true},
		{"src/app.ts", false},
		{"src/app.vue", false},
	}
	for _, tt := range tests {
		if got := isSvelteFile(tt.path); got != tt.want {
			t.Errorf("isSvelteFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
