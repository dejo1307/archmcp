package kafka

import (
	"testing"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/vocab"
	"github.com/enola-labs/enola/pkg/plugin"
)

type fakeInput struct {
	plugin.SignalInput
	facts []facts.Fact
}

func (f fakeInput) Facts() []facts.Fact { return f.facts }
func (f fakeInput) ResolveRepo(candidate string) (string, bool) {
	if candidate == "svc-orders" {
		return candidate, true
	}
	return "", false
}

type fakeEdge struct{}

func (fakeEdge) Via(string)                    {}
func (fakeEdge) Confidence(string)             {}
func (fakeEdge) Sample(plugin.Bucket, string)  {}
func (fakeEdge) Unverified(plugin.Bucket, int) {}

type fakeSink struct {
	plugin.EvidenceSink
	edges int
}

func (s *fakeSink) Edge(_, _ string) plugin.EdgeEvidence { s.edges++; return fakeEdge{} }

func topic(protocol, role string) facts.Fact {
	return facts.Fact{Kind: facts.KindStorage, Name: "svc-orders.order_created", Repo: "svc-billing",
		Props: map[string]any{"storage_kind": facts.StorageKindTopic, "messaging": protocol, "messaging_role": role}}
}

func TestExplicitProtocolAndRoleGateKafkaLinking(t *testing.T) {
	sink := &fakeSink{}
	New(vocab.Default()).Contribute(fakeInput{facts: []facts.Fact{
		topic("mqtt", facts.MessagingRoleConsumer),
		topic("kafka", facts.MessagingRoleProducer),
		topic("kafka", facts.MessagingRoleConsumer),
		topic("kafka-secure", facts.MessagingRoleConsumer),
	}}, sink)
	if sink.edges != 2 {
		t.Fatalf("Kafka and secure Kafka consumers should link, got %d edges", sink.edges)
	}
}

func TestKafkaProtocolFamily(t *testing.T) {
	tests := map[string]bool{
		"kafka": true, "KAFKA": true, " kafka-secure ": true,
		"mqtt": false, "kafkaish": false, "": false,
	}
	for protocol, want := range tests {
		if got := isKafkaProtocol(protocol); got != want {
			t.Errorf("isKafkaProtocol(%q) = %v, want %v", protocol, got, want)
		}
	}
}
