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
// for every Kafka topic a Go file names. Configuration-only references retain no
// direction, while Kafka publish/subscribe call sites carry the shared messaging
// operation contract and their enclosing code symbol. It resolves the two ways a topic
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
				"storage_kind":      facts.StorageKindTopic,
				facts.PropMessaging: "kafka",
				"language":          "go",
				"source":            source,
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

	out = append(out, extractKafkaOperationFacts(fset, f, relFile, pkgDir)...)
	return out
}

var kafkaPublishMethods = map[string]bool{
	"Publish": true, "Produce": true, "SendMessage": true, "WriteMessages": true,
}

var kafkaSubscribeMethods = map[string]bool{
	"Subscribe": true, "Consume": true, "ConsumeTopic": true, "ConsumePartition": true,
}

// extractKafkaOperationFacts recognizes conservative Kafka call-site shapes. The
// whole pass is gated by an imported Kafka client package, which keeps identically
// named in-process Publish/Subscribe methods out of the architecture graph.
func extractKafkaOperationFacts(fset *token.FileSet, f *ast.File, relFile, pkgDir string) []facts.Fact {
	if !importsKafkaClient(f) {
		return nil
	}
	packageBindings := kafkaPackageStringBindings(f)
	seen := map[string]bool{}
	var out []facts.Fact
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		bindings := make(map[string]string, len(packageBindings))
		for name, value := range packageBindings {
			bindings[name] = value
		}
		collectKafkaStringBindings(fn.Body, bindings)
		symbol := pkgDir + "." + fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			symbol = pkgDir + "." + typeExprToString(fn.Recv.List[0].Type) + "." + fn.Name.Name
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			operation, role := "", ""
			switch {
			case kafkaPublishMethods[sel.Sel.Name]:
				operation, role = facts.MessagingOperationPublish, facts.MessagingRoleProducer
			case kafkaSubscribeMethods[sel.Sel.Name]:
				operation, role = facts.MessagingOperationSubscribe, facts.MessagingRoleConsumer
			default:
				return true
			}
			topic := kafkaCallTopic(call, sel.Sel.Name, bindings)
			if topic == "" || !plausibleKafkaTopic(topic) {
				return true
			}
			line := fset.Position(call.Pos()).Line
			key := topic + "\x00" + operation + "\x00" + strconv.Itoa(line)
			if seen[key] {
				return true
			}
			seen[key] = true
			out = append(out, facts.Fact{
				Kind: facts.KindStorage, Name: topic, File: relFile, Line: line,
				Props: map[string]any{
					"storage_kind": facts.StorageKindTopic, facts.PropMessaging: "kafka",
					facts.PropMessagingRole: role, facts.PropMessagingOperation: operation,
					facts.PropSource: facts.MessagingSourceGoKafkaCall,
					"language":       "go", "code_symbol": symbol, "call": sel.Sel.Name,
				},
				Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: pkgDir}},
			})
			return true
		})
	}
	return out
}

func importsKafkaClient(f *ast.File) bool {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err == nil && (strings.Contains(path, "kafka") || strings.Contains(path, "sarama")) {
			return true
		}
	}
	return false
}

func kafkaPackageStringBindings(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			if values, ok := spec.(*ast.ValueSpec); ok {
				collectKafkaValueSpec(values, out)
			}
		}
	}
	return out
}

func collectKafkaStringBindings(root ast.Node, out map[string]string) {
	ambiguous := map[string]bool{}
	ast.Inspect(root, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			collectKafkaValueSpec(node, out)
		case *ast.AssignStmt:
			for i, lhs := range node.Lhs {
				id, ok := lhs.(*ast.Ident)
				if ok && i < len(node.Rhs) && !ambiguous[id.Name] {
					if value := kafkaTopicExpr(node.Rhs[i], out); value != "" {
						if _, exists := out[id.Name]; exists {
							delete(out, id.Name)
							ambiguous[id.Name] = true
						} else {
							out[id.Name] = value
						}
					}
				}
			}
		}
		return true
	})
}

func collectKafkaValueSpec(node *ast.ValueSpec, out map[string]string) {
	for i, name := range node.Names {
		if i < len(node.Values) {
			if value := kafkaTopicExpr(node.Values[i], out); value != "" {
				out[name.Name] = value
			}
		}
	}
}

func kafkaCallTopic(call *ast.CallExpr, method string, bindings map[string]string) string {
	for _, arg := range call.Args {
		if topic := topicFromComposite(arg, bindings); topic != "" {
			return topic
		}
	}
	// These APIs take the topic as their first argument. Producer APIs such as
	// SendMessage/Produce and kafka-go WriteMessages carry it in a message struct;
	// falling back to their first argument would mistake a payload/context for a topic.
	if len(call.Args) > 0 && (method == "Publish" || method == "Subscribe" || method == "Consume" ||
		method == "ConsumeTopic" || method == "ConsumePartition") {
		return kafkaTopicExpr(call.Args[0], bindings)
	}
	return ""
}

func topicFromComposite(expr ast.Expr, bindings map[string]string) string {
	switch node := expr.(type) {
	case *ast.UnaryExpr:
		return topicFromComposite(node.X, bindings)
	case *ast.CompositeLit:
		for _, elt := range node.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Topic" {
				if topic := kafkaTopicExpr(kv.Value, bindings); topic != "" {
					return topic
				}
			}
			if topic := topicFromComposite(kv.Value, bindings); topic != "" {
				return topic
			}
		}
	}
	return ""
}

func kafkaTopicExpr(expr ast.Expr, bindings map[string]string) string {
	if unary, ok := expr.(*ast.UnaryExpr); ok {
		return kafkaTopicExpr(unary.X, bindings)
	}
	if value, ok := stringLitValue(expr); ok {
		return value
	}
	if id, ok := expr.(*ast.Ident); ok {
		return bindings[id.Name]
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if value, ok := envGetTopicDefault(call); ok {
			return value
		}
	}
	return ""
}

func plausibleKafkaTopic(topic string) bool {
	topic = strings.TrimSpace(topic)
	return topic != "" && len(topic) <= 249 && !strings.ContainsAny(topic, " /\\:?#")
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
