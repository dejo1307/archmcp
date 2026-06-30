package cppextractor

import (
	"fmt"
	"os"
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
	c "github.com/tree-sitter/tree-sitter-c/bindings/go"
)

func TestProbeDa8xxTop(t *testing.T) {
	for _, f := range []string{
		"/Users/dejan/development/cpp/linux/arch/arm/mach-davinci/da8xx-dt.c",
		"/Users/dejan/development/cpp/linux/arch/arm/mach-omap2/board-generic.c",
	} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Skip("no tree")
		}
		parser := sitter.NewParser()
		_ = parser.SetLanguage(sitter.NewLanguage(c.Language()))
		tree := parser.Parse(src, nil)
		root := tree.RootNode()
		fmt.Printf("=== %s ===\n", f)
		for i := uint(0); i < root.ChildCount(); i++ {
			ch := root.Child(i)
			line := strings.SplitN(string(src[ch.StartByte():ch.EndByte()]), "\n", 2)[0]
			if len(line) > 50 {
				line = line[:50]
			}
			if strings.Contains(string(src[ch.StartByte():ch.EndByte()]), "MACHINE_START") || ch.Kind() == "ERROR" {
				fmt.Printf("  [%d] %s | %q\n", ch.StartPosition().Row+1, ch.Kind(), line)
			}
		}
		parser.Close()
		tree.Close()
	}
}
