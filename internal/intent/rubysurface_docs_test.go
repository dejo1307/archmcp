package intent

import (
	"os"
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
	src, err := os.ReadFile("../../docs/INTENT.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := docBlock.FindAllStringSubmatch(string(src), -1)
	if len(blocks) < 2 {
		t.Fatalf("the standard should print at least the example and the catalogue, found %d", len(blocks))
	}
	for i, block := range blocks {
		file, problems := ParseRubySurface([]byte(block[1]), "docs/INTENT.md")
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
