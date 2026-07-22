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

func keys(m map[string]facts.Fact) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
