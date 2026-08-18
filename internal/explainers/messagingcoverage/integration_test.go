package messagingcoverage_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enola-labs/enola/pkg/bootstrap"
)

func TestEndToEndAsyncAPICoverageFindings(t *testing.T) {
	repo := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"name":"events","dependencies":{"kafkajs":"1.0.0"}}`)
	write("tsconfig.json", `{}`)
	write("asyncapi.yaml", `asyncapi: 3.0.0
info: {title: Events, version: 1.0.0}
servers:
  broker: {host: localhost:9092, protocol: kafka}
channels:
  declared: {address: orders.declared, servers: [{$ref: '#/servers/broker'}]}
  missingCode: {address: orders.missing-code, servers: [{$ref: '#/servers/broker'}]}
operations:
  publishDeclared: {action: send, channel: {$ref: '#/channels/declared'}}
  publishMissingCode: {action: send, channel: {$ref: '#/channels/missingCode'}}
`)
	write("events.ts", `import { Kafka } from 'kafkajs'
export async function publish(producer: any) {
  await producer.send({topic: 'orders.declared', messages: []})
  await producer.send({topic: 'orders.undeclared', messages: []})
}`)

	eng, cfg, err := bootstrap.NewEngine(bootstrap.Options{ConfigPath: filepath.Join(t.TempDir(), "missing.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Repo = repo
	snapshot, err := eng.GenerateSnapshot(context.Background(), repo, false)
	if err != nil {
		t.Fatal(err)
	}
	var undeclared, unimplemented bool
	for _, insight := range snapshot.Insights {
		if insight.Source != "messaging-coverage" {
			continue
		}
		undeclared = undeclared || strings.Contains(insight.Title, "Undeclared messaging operations")
		unimplemented = unimplemented || strings.Contains(insight.Title, "Unimplemented messaging contract candidates")
	}
	if !undeclared || !unimplemented {
		t.Fatalf("messaging coverage findings: undeclared=%v unimplemented=%v insights=%+v", undeclared, unimplemented, snapshot.Insights)
	}
}
