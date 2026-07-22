package goextractor

import (
	"go/ast"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
)

// extractKafkaFacts emits a topic-reference fact (KindStorage, storage_kind=topic)
// for every Kafka topic a Go file names. It does not try to classify producer vs
// consumer — topic names are conventionally prefixed with the owning service, so the
// cross-repo async linker derives direction from ownership (a repo referencing
// another service's topic is a consumer of it). It resolves the two ways a topic
// string reaches the code without a bare literal at the call site:
//
//   - a config struct field whose name ends in "Topic" carrying an envconfig
//     `default:"<topic>"` tag (the value used unless the env var overrides it), and
//   - an `env.Get("<KEY>_TOPIC", "<topic>")` lookup, whose second argument is the
//     default topic.
//
// Both anchor on an explicit "topic" marker (field-name suffix / env-key substring),
// so an in-process event bus — whose Subscribe takes a Go event symbol, not a topic
// string — never produces a fact here, and is thereby excluded by construction.
func extractKafkaFacts(fset *token.FileSet, f *ast.File, relFile, pkgDir string) []facts.Fact {
	var out []facts.Fact
	seen := map[string]bool{}

	emit := func(topic string, line int, source string) {
		topic = strings.TrimSpace(topic)
		if topic == "" || seen[topic] {
			return
		}
		seen[topic] = true
		out = append(out, facts.Fact{
			Kind: facts.KindStorage,
			Name: topic,
			File: relFile,
			Line: line,
			Props: map[string]any{
				"storage_kind": facts.StorageKindTopic,
				"messaging":    "kafka",
				"language":     "go",
				"source":       source,
			},
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: pkgDir}},
		})
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Field:
			// A config field whose name ends in "Topic" with a default tag: the
			// default is the topic used absent an env override.
			if node.Tag == nil || !fieldNameHasTopicSuffix(node.Names) {
				return true
			}
			tag, err := strconv.Unquote(node.Tag.Value)
			if err != nil {
				return true
			}
			if def := reflect.StructTag(tag).Get("default"); def != "" {
				emit(def, fset.Position(node.Pos()).Line, "config_default")
			}
		case *ast.CallExpr:
			// env.Get("<...>_TOPIC", "<default topic>")
			if topic, ok := envGetTopicDefault(node); ok {
				emit(topic, fset.Position(node.Pos()).Line, "env_default")
			}
		}
		return true
	})

	return out
}

// fieldNameHasTopicSuffix reports whether any of a struct field's names ends in
// "Topic" (e.g. AttributeEventTopic), the marker that its string value is a topic.
func fieldNameHasTopicSuffix(names []*ast.Ident) bool {
	for _, n := range names {
		if strings.HasSuffix(n.Name, "Topic") {
			return true
		}
	}
	return false
}

// envGetTopicDefault recognizes env.Get("<KEY>", "<default>") where the key names a
// topic (contains "TOPIC"), returning the default-topic literal. The env-var default
// is what the running service uses unless overridden, so it is the topic to bind on.
func envGetTopicDefault(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Get" || len(call.Args) != 2 {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "env" {
		return "", false
	}
	key, ok := stringLitValue(call.Args[0])
	if !ok || !strings.Contains(strings.ToUpper(key), "TOPIC") {
		return "", false
	}
	def, ok := stringLitValue(call.Args[1])
	if !ok {
		return "", false
	}
	return def, true
}
