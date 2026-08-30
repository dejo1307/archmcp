package tsextractor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// TestVueCorpusProbe is a skipped-by-default smoke probe for a real Vue/Nuxt
// repository. Example:
//
//	VUE_CORPUS=/path/to/nuxt-ui go test ./internal/extractors/tsextractor \
//	  -run TestVueCorpusProbe -v
func TestVueCorpusProbe(t *testing.T) {
	root := os.Getenv("VUE_CORPUS")
	if root == "" {
		t.Skip("corpus probe disabled; set VUE_CORPUS=<Vue repository> to run")
	}
	var files []string
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".nuxt", "dist", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ff, err := New().Extract(context.Background(), root, files)
	if err != nil {
		t.Fatal(err)
	}
	var components, componentCalls, macroComponents, contractComponents, composableCalls, routes int
	for _, f := range ff {
		if f.Kind == facts.KindRoute {
			routes++
		}
		for _, relation := range f.Relations {
			if relation.Kind == facts.RelCalls && strings.Contains(filepath.ToSlash(relation.Target), "composables.") {
				composableCalls++
			}
		}
		if f.Kind != facts.KindSymbol || f.PropString("web_component") != "component" {
			continue
		}
		components++
		if _, ok := f.Props["vue_macros"]; ok {
			macroComponents++
		}
		if _, ok := f.Props["vue_contract_types"]; ok {
			contractComponents++
		}
		for _, relation := range f.Relations {
			if relation.Kind == facts.RelCalls {
				componentCalls++
			}
		}
	}
	t.Logf("files=%d facts=%d components=%d component_calls=%d macro_components=%d contract_components=%d composable_calls=%d routes=%d",
		len(files), len(ff), components, componentCalls, macroComponents, contractComponents, composableCalls, routes)
}
