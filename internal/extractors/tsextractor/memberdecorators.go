package tsextractor

import (
	"sort"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/enola-labs/enola/internal/extractors/tsutil"
	"github.com/enola-labs/enola/internal/facts"
)

func ownDecoratorNames(kinds *tsutil.KindTable, node *sitter.Node, src []byte) []string {
	var names []string
	for i := range node.ChildCount() {
		c := node.Child(i)
		if kindOf(kinds, c) != "decorator" {
			continue
		}
		if name, _ := decoratorNameArgs(kinds, c, src); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func classDecoratorNames(kinds *tsutil.KindTable, classNode *sitter.Node, src []byte) string {
	names := ownDecoratorNames(kinds, classNode, src)
	if parent := classNode.Parent(); parent != nil && kindOf(kinds, parent) == "export_statement" {
		names = append(names, ownDecoratorNames(kinds, parent, src)...)
	}
	return decoratorSetProp(names)
}

func decoratorSetProp(names []string) string {
	if len(names) == 0 {
		return ""
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if strings.ContainsAny(n, " \t\n") {
			continue
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func isGetterDefinition(kinds *tsutil.KindTable, member *sitter.Node) bool {
	if kindOf(kinds, member) != "method_definition" {
		return false
	}
	name := member.ChildByFieldName("name")
	for i := range member.ChildCount() {
		c := member.Child(i)
		if name != nil && c.StartByte() >= name.StartByte() {
			break
		}
		if kindOf(kinds, c) == "get" {
			return true
		}
	}
	return false
}

func countCallRelations(rels []facts.Relation) int {
	n := 0
	for _, r := range rels {
		if r.Kind == facts.RelCalls {
			n++
		}
	}
	return n
}
