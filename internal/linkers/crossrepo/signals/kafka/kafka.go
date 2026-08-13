// Package kafka links repos coupled by Kafka topics.
package kafka

import (
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/internal/linkers/vocab"
	"github.com/enola-labs/enola/pkg/plugin"
)

// Signal binds ASYNCHRONOUS coupling: a repo that references a Kafka topic owned by
// another loaded repo consumes that repo's events, so it depends on it. The edge is
// drawn consumer -> producer, mirroring HTTP's client -> server.
//
// It is directional despite the transport being fire-and-forget: topic ownership is
// what orients it, and ownership is asymmetric.
type Signal struct {
	v *vocab.Set
}

// New returns the signal, reading topic ownership under the given vocabulary.
func New(v *vocab.Set) *Signal { return &Signal{v: v} }

func (s *Signal) Name() string { return "kafka" }

func (s *Signal) Phase() plugin.SignalPhase { return plugin.PhaseDirectional }

// topicOwner returns the owning-service segment of a Kafka topic name: the part
// before the first ".". By convention a topic is named "<owning-service>.<...>"
// (e.g. "svc-orders.order_placed" -> "svc-orders",
// "core.items.item_uploaded" -> "core"), so the leading segment identifies the
// producer even when that repo's producer code cannot be parsed.
func (s *Signal) topicOwner(topic string) string {
	topic = strings.TrimSpace(topic)
	if i := strings.Index(topic, s.v.TopicOwnerSeparator); i >= 0 {
		return topic[:i]
	}
	return topic
}

// linkKafka binds asynchronous coupling: a repo that references a Kafka topic owned
// by ANOTHER loaded repo consumes that repo's events, so it depends on it. The edge
// is drawn consumer -> producer (mirroring HTTP client -> server), keyed on the
// topic name's owning-service prefix — the same leading-segment-to-repo resolution
// linkImports uses, and robust to the producer side being unparsed. A repo's
// reference to its OWN topic (owner == repo) is intra-repo and draws no edge, and a
// topic owned by no loaded repo (an export sink, a third-party service) is simply
// left unlinked.
func (s *Signal) Contribute(in plugin.SignalInput, out plugin.EvidenceSink) {
	for _, f := range in.Facts() {
		if f.Repo == "" || f.Kind != facts.KindStorage {
			continue
		}
		if f.PropString("storage_kind") != facts.StorageKindTopic {
			continue
		}
		// Historically every topic fact came from Kafka-aware code extraction.
		// AsyncAPI introduces other protocols into the same fact kind; an explicit
		// non-Kafka protocol must not manufacture a via=kafka dependency.
		if protocol := f.PropString(facts.PropMessaging); protocol != "" && !isKafkaProtocol(protocol) {
			continue
		}
		// A contract can state direction explicitly. Publishing to a topic is an
		// outbound interface, not evidence that this repo consumes the topic owner.
		// Older extractors emit no role and retain name-based inference.
		if f.PropString(facts.PropMessagingRole) == facts.MessagingRoleProducer {
			continue
		}
		owner := s.topicOwner(f.Name)
		if owner == "" {
			continue
		}
		label, ok := in.ResolveRepo(owner)
		if !ok || label == f.Repo {
			continue // owner not loaded, or the repo's own topic (it is the producer)
		}
		e := out.Edge(f.Repo, label)
		e.Via(facts.ViaKafka)
		e.Sample(plugin.BucketTopics, f.Name)
	}
}

// isKafkaProtocol classifies AsyncAPI's Kafka-family protocol labels. The
// security suffix describes broker authentication/transport security (for
// example SASL/SCRAM or mTLS), not a different messaging technology.
func isKafkaProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "kafka", "kafka-secure":
		return true
	default:
		return false
	}
}
