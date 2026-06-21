package tsextractor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func setupVueProject(t *testing.T, files map[string]string, nuxt bool) string {
	t.Helper()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkgJSON := `{"dependencies": {"vue": "^3.0.0"}}`
	if nuxt {
		pkgJSON = `{"dependencies": {"vue": "^3.0.0", "nuxt": "^3.0.0"}}`
		if err := os.WriteFile(filepath.Join(dir, "nuxt.config.ts"), []byte(`export default defineNuxtConfig({})`), 0o644); err != nil {
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

func extractVue(t *testing.T, files map[string]string, nuxt bool) []facts.Fact {
	t.Helper()
	dir := setupVueProject(t, files, nuxt)

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

func TestExtractVueScriptBlocks_Setup(t *testing.T) {
	src := []byte(`<template>
  <div>{{ msg }}</div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
const msg = ref('hello')
</script>
`)
	blocks := extractVueScriptBlocks(src)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !blocks[0].IsSetup {
		t.Error("expected IsSetup = true")
	}
	if blocks[0].Lang != "ts" {
		t.Errorf("lang = %q, want ts", blocks[0].Lang)
	}
	if blocks[0].StartLine != 4 {
		t.Errorf("StartLine = %d, want 4", blocks[0].StartLine)
	}
}

func TestExtractVueScriptBlocks_NoSetup(t *testing.T) {
	src := []byte(`<script lang="ts">
import { defineComponent } from 'vue'
export default defineComponent({ name: 'MyComp' })
</script>
`)
	blocks := extractVueScriptBlocks(src)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].IsSetup {
		t.Error("expected IsSetup = false")
	}
	if blocks[0].Lang != "ts" {
		t.Errorf("lang = %q, want ts", blocks[0].Lang)
	}
}

func TestExtractVueScriptBlocks_Multiple(t *testing.T) {
	src := []byte(`<script lang="ts">
export interface Props { title: string }
</script>

<script setup lang="ts">
const props = defineProps<Props>()
</script>
`)
	blocks := extractVueScriptBlocks(src)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].IsSetup {
		t.Error("first block should not be setup")
	}
	if !blocks[1].IsSetup {
		t.Error("second block should be setup")
	}
}

func TestExtractVueScriptBlocks_NoScript(t *testing.T) {
	src := []byte(`<template><div>Hello</div></template>`)
	blocks := extractVueScriptBlocks(src)
	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks, got %d", len(blocks))
	}
}

// --- Framework detection ---

func TestDetectVue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"dependencies":{"vue":"^3.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !detectVue(dir) {
		t.Error("expected detectVue = true")
	}
}

func TestDetectNuxt_Config(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nuxt.config.ts"), []byte(`export default {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !detectNuxt(dir) {
		t.Error("expected detectNuxt = true")
	}
}

func TestDetectNuxt_PkgDep(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"devDependencies":{"nuxt":"^3.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !detectNuxt(dir) {
		t.Error("expected detectNuxt = true")
	}
}

// --- Nuxt route detection ---

func TestDetectNuxtRoute(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		wantRoute string
	}{
		{"index page", "pages/index.vue", "/"},
		{"about page", "pages/about.vue", "/about"},
		{"nested page", "pages/users/index.vue", "/users"},
		{"dynamic param", "pages/users/[id].vue", "/users/[id]"},
		{"deep nested", "pages/blog/posts/[slug].vue", "/blog/posts/[slug]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectNuxtRoute(tt.file)
			if got == nil {
				t.Fatal("expected route fact, got nil")
			}
			if got.Name != tt.wantRoute {
				t.Errorf("route = %q, want %q", got.Name, tt.wantRoute)
			}
			if got.Props["framework"] != "nuxt" {
				t.Errorf("framework = %v, want nuxt", got.Props["framework"])
			}
			if got.Props["router"] != "pages" {
				t.Errorf("router = %v, want pages", got.Props["router"])
			}
		})
	}
}

func TestDetectNuxtRoute_NonPage(t *testing.T) {
	if got := detectNuxtRoute("src/components/Button.vue"); got != nil {
		t.Error("non-page .vue file should return nil")
	}
	if got := detectNuxtRoute("pages/about.ts"); got != nil {
		t.Error(".ts file should return nil")
	}
}

// --- Full extraction: Vue SFC ---

func TestExtract_VueSFC_ScriptSetup(t *testing.T) {
	ff := extractVue(t, map[string]string{
		"src/components/Counter.vue": `<template>
  <button @click="increment">{{ count }}</button>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const count = ref(0)

function increment() {
  count.value++
}
</script>
`,
	}, false)

	// Component fact
	comp, ok := findFact(ff, "src/components.Counter")
	if !ok {
		t.Fatalf("expected component fact src/components.Counter; got %v", factNames(ff))
	}
	if comp.Props["web_component"] != "component" {
		t.Errorf("web_component = %v, want component", comp.Props["web_component"])
	}
	if comp.Props["framework"] != "vue" {
		t.Errorf("framework = %v, want vue", comp.Props["framework"])
	}
	if comp.Props["vue_setup"] != true {
		t.Errorf("vue_setup = %v, want true", comp.Props["vue_setup"])
	}

	// Declarations from script block
	inc, ok := findFact(ff, "src/components.increment")
	if !ok {
		t.Fatalf("expected fact src/components.increment; got %v", factNames(ff))
	}
	if inc.Props["symbol_kind"] != facts.SymbolFunc {
		t.Errorf("increment symbol_kind = %v, want function", inc.Props["symbol_kind"])
	}

	// Import dependency
	deps := findFactsByKind(ff, facts.KindDependency)
	hasVueImport := false
	for _, d := range deps {
		for _, r := range d.Relations {
			if r.Target == "vue" {
				hasVueImport = true
			}
		}
	}
	if !hasVueImport {
		t.Error("expected import dependency on 'vue'")
	}
}

func TestExtract_VueSFC_DefineComponent(t *testing.T) {
	ff := extractVue(t, map[string]string{
		"src/components/Modal.vue": `<template>
  <div class="modal"><slot/></div>
</template>

<script lang="ts">
import { defineComponent } from 'vue'

export default defineComponent({
  name: 'Modal',
  props: { visible: Boolean },
})
</script>
`,
	}, false)

	comp, ok := findFact(ff, "src/components.Modal")
	if !ok {
		t.Fatalf("expected component fact src/components.Modal; got %v", factNames(ff))
	}
	if comp.Props["web_component"] != "component" {
		t.Errorf("web_component = %v, want component", comp.Props["web_component"])
	}
	if comp.Props["framework"] != "vue" {
		t.Errorf("framework = %v, want vue", comp.Props["framework"])
	}
	if comp.Props["vue_setup"] == true {
		t.Error("vue_setup should not be true for non-setup script")
	}
}

func TestExtract_VueSFC_TemplateOnly(t *testing.T) {
	ff := extractVue(t, map[string]string{
		"src/components/Divider.vue": `<template><hr/></template>`,
	}, false)

	comp, ok := findFact(ff, "src/components.Divider")
	if !ok {
		t.Fatalf("expected component fact src/components.Divider; got %v", factNames(ff))
	}
	if comp.Props["web_component"] != "component" {
		t.Errorf("web_component = %v, want component", comp.Props["web_component"])
	}
}

func TestExtract_VueSFC_LineOffset(t *testing.T) {
	ff := extractVue(t, map[string]string{
		"src/components/App.vue": `<template>
  <div>hello</div>
</template>

<script setup lang="ts">
function greet() { return 'hi' }
</script>
`,
	}, false)

	f, ok := findFact(ff, "src/components.greet")
	if !ok {
		t.Fatalf("expected fact src/components.greet; got %v", factNames(ff))
	}
	// greet is on line 6 of the .vue file (1-based), script block starts at line 5
	if f.Line < 5 {
		t.Errorf("line = %d, expected >= 5 (script block offset should be applied)", f.Line)
	}
}

// --- Composable classification ---

func TestExtract_VueComposable(t *testing.T) {
	ff := extractVue(t, map[string]string{
		"src/composables/useAuth.ts": `export function useAuth() { return null }
export const useUser = () => null`,
	}, false)

	for _, name := range []string{"src/composables.useAuth", "src/composables.useUser"} {
		f, ok := findFact(ff, name)
		if !ok {
			t.Fatalf("expected fact %s", name)
		}
		if f.Props["web_component"] != "composable" {
			t.Errorf("%s web_component = %v, want composable", name, f.Props["web_component"])
		}
		if f.Props["framework"] != "vue" {
			t.Errorf("%s framework = %v, want vue", name, f.Props["framework"])
		}
	}
}

func TestExtract_NuxtComposable(t *testing.T) {
	ff := extractVue(t, map[string]string{
		"src/composables/useFetch.ts": `export function useFetch() { return null }`,
	}, true)

	f, ok := findFact(ff, "src/composables.useFetch")
	if !ok {
		t.Fatalf("expected fact src/composables.useFetch; got %v", factNames(ff))
	}
	if f.Props["web_component"] != "composable" {
		t.Errorf("web_component = %v, want composable", f.Props["web_component"])
	}
	if f.Props["framework"] != "nuxt" {
		t.Errorf("framework = %v, want nuxt", f.Props["framework"])
	}
}

// --- Nuxt page extraction ---

func TestExtract_NuxtPage(t *testing.T) {
	ff := extractVue(t, map[string]string{
		"pages/about.vue": `<template><h1>About</h1></template>

<script setup lang="ts">
const title = 'About'
</script>
`,
	}, true)

	// Component fact
	comp, ok := findFact(ff, "pages.About")
	if !ok {
		t.Fatalf("expected component fact pages.About; got %v", factNames(ff))
	}
	if comp.Props["framework"] != "nuxt" {
		t.Errorf("framework = %v, want nuxt", comp.Props["framework"])
	}

	// Route fact
	routes := findFactsByKind(ff, facts.KindRoute)
	var found bool
	for _, r := range routes {
		if r.Name == "/about" && r.Props["framework"] == "nuxt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected nuxt route /about; routes: %v", routes)
	}
}

// --- Vue Router config detection ---

func TestExtract_VueRouterConfig(t *testing.T) {
	ff := extractVue(t, map[string]string{
		"src/router/index.ts": `import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(),
  routes: [],
})

export default router`,
	}, false)

	routes := findFactsByKind(ff, facts.KindRoute)
	var found bool
	for _, r := range routes {
		if r.Props["type"] == "router_config" && r.Props["framework"] == "vue" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected vue router_config route fact; routes: %v", routes)
	}
}

// --- isVueFile ---

func TestIsVueFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"src/App.vue", true},
		{"src/app.VUE", true},
		{"src/app.ts", false},
		{"src/app.tsx", false},
	}
	for _, tt := range tests {
		if got := isVueFile(tt.path); got != tt.want {
			t.Errorf("isVueFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// --- isTypeScriptFile includes .vue ---

func TestIsTypeScriptFile_Vue(t *testing.T) {
	if !isTypeScriptFile("src/App.vue") {
		t.Error("isTypeScriptFile should return true for .vue files")
	}
}
