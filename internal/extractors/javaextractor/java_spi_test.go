package javaextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

// TestJavaSPIServiceFileRefs: a Dubbo/JDK SPI service file registers an impl class
// by fully-qualified name; the extractor must fold that registration in as a
// KindFileRef reference so the impl (loaded by name via ExtensionLoader/ServiceLoader,
// never called in code) is not reported as a dead-code orphan. An entry that does not
// resolve to an in-repo type (e.g. java.lang.String) yields no reference.
func TestJavaSPIServiceFileRefs(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/main/java/com/example/spi/RandomBalance.java": `package com.example.spi;
public class RandomBalance implements LoadBalance {}
`,
		"src/main/resources/META-INF/dubbo/internal/com.example.spi.LoadBalance": `# leading comment
random=com.example.spi.RandomBalance

builtin=java.lang.String
`,
	})

	const impl = "src/main/java/com/example/spi.RandomBalance"
	refs := factsByKind(ff, facts.KindFileRef)
	if len(refs) == 0 {
		t.Fatal("no KindFileRef facts emitted for the SPI service file")
	}
	found := false
	for _, r := range refs {
		if hasRelation(r, facts.RelCalls, impl) {
			found = true
		}
		if hasRelation(r, facts.RelCalls, "java.lang.String") {
			t.Error("external SPI entry java.lang.String must not be folded as a reference")
		}
	}
	if !found {
		t.Errorf("SPI impl %s has no KindFileRef RelCalls reference; it would be a false orphan", impl)
	}
}

// TestJavaRuleNodeProp: a class annotated @RuleNode (a ThingsBoard rule-engine node,
// discovered at runtime by classpath scanning, with no in-code caller) must carry
// scanned_plugin=true so the orphan detector treats it as a framework entry point.
func TestJavaRuleNodeProp(t *testing.T) {
	ff := extractAll(t, map[string]string{
		"src/main/java/com/example/rules/MyNode.java": `package com.example.rules;
@RuleNode(type = ComponentType.ACTION, name = "My Node")
public class MyNode implements TbNode {}
`,
		// A plain class carrying no scanned-plugin annotation must NOT get the prop.
		"src/main/java/com/example/rules/Plain.java": `package com.example.rules;
public class Plain {}
`,
	})

	node, ok := findFactKind(ff, facts.KindSymbol, "src/main/java/com/example/rules.MyNode")
	if !ok {
		t.Fatal("MyNode symbol fact not found")
	}
	if node.Props["scanned_plugin"] != true {
		t.Errorf("MyNode scanned_plugin = %v, want true", node.Props["scanned_plugin"])
	}
	plain, ok := findFactKind(ff, facts.KindSymbol, "src/main/java/com/example/rules.Plain")
	if !ok {
		t.Fatal("Plain symbol fact not found")
	}
	if plain.Props["scanned_plugin"] != nil {
		t.Errorf("Plain scanned_plugin = %v, want nil (no over-tagging)", plain.Props["scanned_plugin"])
	}
}
