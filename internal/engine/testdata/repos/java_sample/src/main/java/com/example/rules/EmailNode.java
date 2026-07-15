package com.example.rules;

// Discovered at runtime by classpath scanning for @RuleNode — no in-code caller.
// The scanned_plugin prop keeps it off the dead-code list.
@RuleNode(type = ComponentType.ACTION, name = "Email")
public class EmailNode {
    public void process() {
    }
}
