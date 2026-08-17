package tsextractor

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestExtractTSKafkaCallSites(t *testing.T) {
	got := extractAll(t, map[string]string{
		"src/events.ts": `import { Kafka } from 'kafkajs'
const CREATED_TOPIC = 'orders.created'

export async function publishOrder(producer: any) {
  await producer.send({ topic: CREATED_TOPIC, messages: [{ value: '{}' }] })
}

class BillingEvents {
  async start(consumer: any) {
    await consumer.subscribe({ topic: 'orders.created' })
  }
}
`,
	}, false)

	var publish, subscribe *facts.Fact
	for i := range got {
		f := &got[i]
		if f.PropString(facts.PropSource) != facts.MessagingSourceTSKafkaCall {
			continue
		}
		switch f.PropString(facts.PropMessagingOperation) {
		case facts.MessagingOperationPublish:
			publish = f
		case facts.MessagingOperationSubscribe:
			subscribe = f
		}
	}
	if publish == nil || publish.Name != "orders.created" || publish.PropString("code_symbol") != "src.publishOrder" {
		t.Fatalf("publish fact = %+v", publish)
	}
	if subscribe == nil || subscribe.Name != "orders.created" || subscribe.PropString("code_symbol") != "src.BillingEvents.start" {
		t.Fatalf("subscribe fact = %+v", subscribe)
	}
}

func TestExtractTSKafkaRequiresImportAndStaticTopic(t *testing.T) {
	got := extractAll(t, map[string]string{
		"src/local.ts": `export function local(bus: any) {
  // mentioning import { Kafka } from 'kafkajs' is not an actual import
  bus.send({ topic: 'not-kafka' })
  bus.subscribe({ topic: 'not-kafka' })
}`,
		"src/dynamic.ts": "import { Kafka } from 'kafkajs'\nexport function dynamic(producer: any, tenant: string) {\n  producer.send({ topic: `orders.${tenant}`, messages: [] })\n}",
		"src/mutable.ts": `import { Kafka } from 'kafkajs'
let topic = 'first.topic'
topic = 'second.topic'
export function mutable(producer: any) {
  producer.send({ topic, messages: [] })
}`,
		"src/test/events.test.ts": `import { Kafka } from 'kafkajs'
export function fixture(producer: any) {
  producer.send({ topic: 'test-only', messages: [] })
}`,
	}, false)
	for _, f := range got {
		if f.PropString(facts.PropSource) == facts.MessagingSourceTSKafkaCall {
			t.Fatalf("unexpected Kafka fact: %+v", f)
		}
	}
}

func TestExtractTSNodeRDKafkaProduce(t *testing.T) {
	got := extractAll(t, map[string]string{
		"producer.js": `const Kafka = require('node-rdkafka')
const topic = 'audit.events'
const publish = (producer) => producer.produce(topic, null, Buffer.from('{}'))
`,
	}, false)
	for _, f := range got {
		if f.PropString(facts.PropSource) == facts.MessagingSourceTSKafkaCall {
			if f.Name != "audit.events" || f.PropString("code_symbol") != "..publish" {
				t.Fatalf("produce fact = %+v", f)
			}
			return
		}
	}
	t.Fatal("missing node-rdkafka produce fact")
}

func TestExtractTSKafkaConstantsAreFunctionScoped(t *testing.T) {
	got := extractAll(t, map[string]string{"events.ts": `import { Kafka } from 'kafkajs'
function first(p: any) { const topic = 'orders.first'; p.send({topic: topic, messages: []}) }
function second(p: any) { const topic = 'orders.second'; p.send({topic: topic, messages: []}) }
`}, false)
	seen := map[string]bool{}
	for _, f := range got {
		if f.PropString(facts.PropSource) == facts.MessagingSourceTSKafkaCall {
			seen[f.Name] = true
		}
	}
	if !seen["orders.first"] || !seen["orders.second"] {
		t.Fatalf("function-scoped topic constants were dropped: %+v", got)
	}
}
