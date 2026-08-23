package intent

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var docBlock = regexp.MustCompile("(?s)```ruby\n(Enola\\.architecture.*?)```")

// Every Ruby declaration the standard prints must compile, and the catalogue
// must validate as a declaration. A documented law that cannot be read is
// worse than an undocumented one: a reader copies it and learns the surface is
// unreliable.
func TestRubySurface_DocumentedDeclarationsCompile(t *testing.T) {
	// Every documentation page is searched, not one named file. The surface was
	// documented in a single page until that page was split, and a test pinned to
	// its name goes green by finding nothing — the failure mode it exists to catch.
	var blocks [][]string
	var origin []string
	err := filepath.WalkDir("../../docs", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, b := range docBlock.FindAllStringSubmatch(string(src), -1) {
			blocks = append(blocks, b)
			origin = append(origin, filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) < 2 {
		t.Fatalf("the standard should print at least the example and the catalogue, found %d", len(blocks))
	}
	for i, block := range blocks {
		file, problems := ParseRubySurface([]byte(block[1]), origin[i])
		if len(problems) != 0 {
			t.Fatalf("block %d does not compile: %s", i, strings.Join(problems, "; "))
		}
		if len(file.Rules) == 0 {
			t.Fatalf("block %d declares no law", i)
		}
		if err := (&Declaration{Components: file.Components, Rules: file.Rules}).Validate(); err != nil {
			t.Fatalf("block %d does not validate: %v", i, err)
		}
	}
}
