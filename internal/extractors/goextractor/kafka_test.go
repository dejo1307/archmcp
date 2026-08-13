package goextractor

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func kafkaTopics(t *testing.T, src string) map[string]facts.Fact {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	byName := map[string]facts.Fact{}
	for _, ff := range extractKafkaFacts(fset, f, "internal/config/config.go", "internal/config") {
		if ff.Kind == facts.KindStorage && ff.Props["storage_kind"] == facts.StorageKindTopic {
			byName[ff.Name] = ff
		}
	}
	return byName
}

func TestExtractKafkaFacts_ConfigDefaultTag(t *testing.T) {
	// A config struct field whose name ends in Topic, with an envconfig default: the
	// default value is the topic to bind on.
	src := `package config

type Config struct {
	AttributeEventTopic      string ` + "`" + `envconfig:"ATTRIBUTE_EVENT_TOPIC" default:"svc-other.attributes"` + "`" + `
	CacheInvalidationTopic   string ` + "`" + `envconfig:"CACHE_TOPIC" default:"svc-self.cache.v1.invalidation"` + "`" + `
	SomeOtherField           string ` + "`" + `envconfig:"OTHER" default:"not-a-topic"` + "`" + `
}
`
	got := kafkaTopics(t, src)
	if len(got) != 2 {
		t.Fatalf("expected 2 topic facts (only *Topic fields), got %d: %v", len(got), keys(got))
	}
	for _, want := range []string{"svc-other.attributes", "svc-self.cache.v1.invalidation"} {
		f, ok := got[want]
		if !ok {
			t.Errorf("missing topic %q; got %v", want, keys(got))
			continue
		}
		if f.Props["messaging"] != "kafka" || f.Props["source"] != "config_default" {
			t.Errorf("%s props = %v", want, f.Props)
		}
	}
	if _, ok := got["not-a-topic"]; ok {
		t.Errorf("non-Topic field must not produce a topic fact")
	}
}

func TestExtractKafkaFacts_EnvGetDefault(t *testing.T) {
	// env.Get("X_TOPIC", "default") — the second arg is the default topic.
	src := `package config

func wire() {
	p := Publisher{Topic: env.Get("EVENTS_TOPIC", "svc-self.events")}
	_ = p
}
`
	got := kafkaTopics(t, src)
	f, ok := got["svc-self.events"]
	if !ok {
		t.Fatalf("expected topic svc-self.events from env.Get default; got %v", keys(got))
	}
	if f.Props["source"] != "env_default" {
		t.Errorf("source = %v, want env_default", f.Props["source"])
	}
}

func TestExtractKafkaFacts_IgnoresInProcessEventBus(t *testing.T) {
	// An in-process event bus subscribes on a Go symbol, not a topic string, and its
	// env lookups are not topics — nothing here is a Kafka topic reference.
	src := `package app

func wire(bus EventBus) {
	bus.Subscribe(event.CampaignBudgetSpent, handleSpent)
	bus.Subscribe(event.CampaignEnded, handleEnded)
	timeout := env.Get("REQUEST_TIMEOUT", "30s")
	_ = timeout
}
`
	got := kafkaTopics(t, src)
	if len(got) != 0 {
		t.Errorf("in-process event bus / non-topic env.Get must not produce topic facts, got %v", keys(got))
	}
}

func TestExtractKafkaFacts_PublishSubscribeOperations(t *testing.T) {
	src := `package app

import "github.com/segmentio/kafka-go"

const ordersTopic = "svc-orders.order_created"

type Handler struct{}

func (h *Handler) wire(bus *Client) {
	bus.Publish(ordersTopic, payload)
	bus.Subscribe("svc-orders.order_created", handle)
	bus.WriteMessages(ctx, kafka.Message{Topic: "svc-orders.order_audited", Value: payload})
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var operations []facts.Fact
	for _, fact := range extractKafkaFacts(fset, f, "app/events.go", "app") {
		if fact.PropString(facts.PropSource) == facts.MessagingSourceGoKafkaCall {
			operations = append(operations, fact)
		}
	}
	if len(operations) != 3 {
		t.Fatalf("expected 3 Kafka operations, got %d: %+v", len(operations), operations)
	}
	want := map[string]string{
		"svc-orders.order_created\x00publish":   facts.MessagingRoleProducer,
		"svc-orders.order_created\x00subscribe": facts.MessagingRoleConsumer,
		"svc-orders.order_audited\x00publish":   facts.MessagingRoleProducer,
	}
	for _, fact := range operations {
		key := fact.Name + "\x00" + fact.PropString(facts.PropMessagingOperation)
		if fact.PropString(facts.PropMessagingRole) != want[key] {
			t.Errorf("operation %+v does not match expected role %q", fact, want[key])
		}
		if fact.PropString("code_symbol") != "app.Handler.wire" {
			t.Errorf("code_symbol = %q", fact.PropString("code_symbol"))
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Errorf("missing operations: %v", want)
	}
}

func TestExtractKafkaFacts_OperationRequiresKafkaImport(t *testing.T) {
	src := `package app
func wire(bus *EventBus) { bus.Publish("svc-orders.order_created", payload) }
`
	got := kafkaTopics(t, src)
	if len(got) != 0 {
		t.Fatalf("in-process publish without Kafka import emitted facts: %+v", got)
	}
}

func TestExtractKafkaFacts_LocalTopicBindingsStayFunctionScoped(t *testing.T) {
	src := `package app
import "github.com/segmentio/kafka-go"
func publishA(bus *Client) {
	topic := "svc-orders.a"
	bus.Publish(topic, payload)
}
func publishB(bus *Client) {
	topic := "svc-orders.b"
	bus.Publish(topic, payload)
}
var _ kafka.Message
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, fact := range extractKafkaFacts(fset, f, "events.go", ".") {
		if fact.PropString(facts.PropSource) == facts.MessagingSourceGoKafkaCall {
			got[fact.Name] = fact.PropString("code_symbol")
		}
	}
	if got["svc-orders.a"] != "..publishA" || got["svc-orders.b"] != "..publishB" {
		t.Fatalf("function-scoped bindings resolved incorrectly: %v", got)
	}
}

func keys(m map[string]facts.Fact) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
