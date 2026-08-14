package messagingcontract

import (
	"context"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func contract(topic, operation, id, file string) facts.Fact {
	return facts.Fact{Kind: facts.KindStorage, Name: topic, File: file, Repo: "orders", Props: map[string]any{
		"storage_kind": facts.StorageKindTopic, facts.PropSource: facts.MessagingSourceAsyncAPI,
		facts.PropMessagingOperation: operation, "operationId": id,
	}}
}

func code(topic, operation, symbol string, line int) facts.Fact {
	return facts.Fact{Kind: facts.KindStorage, Name: topic, File: "events.go", Line: line, Repo: "orders", Props: map[string]any{
		"storage_kind": facts.StorageKindTopic, facts.PropSource: facts.MessagingSourceGoKafkaCall,
		facts.PropMessagingOperation: operation, "code_symbol": symbol,
	}}
}

func TestBindUniqueOperation(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		contract("orders.created", facts.MessagingOperationPublish, "publishOrder", "asyncapi.yaml"),
		code("orders.created", facts.MessagingOperationPublish, "events.PublishOrder", 12),
	)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatal(err)
	} // idempotence

	all := store.FactsRef()
	for _, f := range all {
		if isCodeOperation(f) {
			if f.Props[facts.PropMessagingContractBound] != true || f.Props[facts.PropMessagingContractOperationID] != "publishOrder" || f.Props[facts.PropMessagingContractFile] != "asyncapi.yaml" {
				t.Errorf("code binding props = %+v", f.Props)
			}
		}
		if isContractOperation(f) {
			if f.Props[facts.PropMessagingImplementationCount] != 1 {
				t.Errorf("contract props = %+v", f.Props)
			}
			if len(f.Relations) != 1 || f.Relations[0].Kind != facts.RelImplementedBy || f.Relations[0].Target != "events.PublishOrder" {
				t.Errorf("contract relations = %+v", f.Relations)
			}
		}
	}
}

func TestAmbiguousAndDirectionMismatchStayUnbound(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		contract("orders.created", facts.MessagingOperationPublish, "one", "one.yaml"),
		contract("orders.created", facts.MessagingOperationPublish, "two", "two.yaml"),
		contract("orders.created", facts.MessagingOperationSubscribe, "consume", "consume.yaml"),
		code("orders.created", facts.MessagingOperationPublish, "events.Publish", 10),
		code("orders.other", facts.MessagingOperationSubscribe, "events.ConsumeOther", 20),
	)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	for _, f := range store.FactsRef() {
		if !isCodeOperation(f) {
			continue
		}
		if f.Props[facts.PropMessagingContractBound] == true {
			t.Errorf("ambiguous/unmatched code operation was bound: %+v", f)
		}
		want := facts.MessagingContractStatusAmbiguous
		if f.Name == "orders.other" {
			want = facts.MessagingContractStatusUndeclared
		}
		if f.PropString(facts.PropMessagingContractStatus) != want {
			t.Errorf("status = %q, want %q: %+v", f.PropString(facts.PropMessagingContractStatus), want, f)
		}
	}
}

func TestBindTypeScriptKafkaCall(t *testing.T) {
	call := code("orders.created", facts.MessagingOperationPublish, "src.publishOrder", 8)
	call.File = "events.ts"
	call.Props[facts.PropSource] = facts.MessagingSourceTSKafkaCall
	store := facts.NewStore()
	store.Add(
		contract("orders.created", facts.MessagingOperationPublish, "publishOrder", "asyncapi.yaml"),
		call,
	)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	for _, f := range store.FactsRef() {
		if f.PropString(facts.PropSource) == facts.MessagingSourceTSKafkaCall && f.Props[facts.PropMessagingContractBound] != true {
			t.Fatalf("TypeScript call not bound: %+v", f)
		}
	}
}

func TestProtocolCompatibility(t *testing.T) {
	tests := []struct {
		name             string
		contractProtocol string
		wantBound        bool
		wantStatus       string
	}{
		{name: "Kafka", contractProtocol: "kafka", wantBound: true, wantStatus: facts.MessagingContractStatusBound},
		{name: "Kafka secure", contractProtocol: "kafka-secure", wantBound: true, wantStatus: facts.MessagingContractStatusBound},
		{name: "unspecified", contractProtocol: "", wantBound: true, wantStatus: facts.MessagingContractStatusBound},
		{name: "MQTT", contractProtocol: "mqtt", wantStatus: facts.MessagingContractStatusProtocolMismatch},
		{name: "AMQP", contractProtocol: "amqp", wantStatus: facts.MessagingContractStatusProtocolMismatch},
		{name: "WebSocket", contractProtocol: "wss", wantStatus: facts.MessagingContractStatusProtocolMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contractFact := contract("shared.events", facts.MessagingOperationPublish, "publishEvent", "asyncapi.yaml")
			if tt.contractProtocol != "" {
				contractFact.Props[facts.PropMessaging] = tt.contractProtocol
			}
			codeFact := code("shared.events", facts.MessagingOperationPublish, "events.Publish", 12)
			codeFact.Props[facts.PropMessaging] = "kafka"
			store := facts.NewStore()
			store.Add(contractFact, codeFact)
			if err := New().Bind(context.Background(), store); err != nil {
				t.Fatal(err)
			}
			bound := false
			status := ""
			for _, f := range store.FactsRef() {
				if isCodeOperation(f) {
					bound = f.Props[facts.PropMessagingContractBound] == true
					status = f.PropString(facts.PropMessagingContractStatus)
				}
			}
			if bound != tt.wantBound {
				t.Fatalf("bound = %v, want %v", bound, tt.wantBound)
			}
			if status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}

func TestBoundCallWithoutSymbolStillImplementsContract(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		contract("orders.created", facts.MessagingOperationPublish, "publishOrder", "asyncapi.yaml"),
		code("orders.created", facts.MessagingOperationPublish, "", 12),
	)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	for _, f := range store.FactsRef() {
		if !isContractOperation(f) {
			continue
		}
		if f.PropString(facts.PropMessagingImplementationStatus) != facts.MessagingImplementationImplemented || f.Props[facts.PropMessagingImplementationCount] != 1 {
			t.Fatalf("contract implementation props = %+v", f.Props)
		}
		if len(f.Relations) != 0 {
			t.Fatalf("anonymous implementation must not fabricate a symbol relation: %+v", f.Relations)
		}
	}
}

func TestProtocolDisambiguatesSameTopicOperations(t *testing.T) {
	kafkaContract := contract("shared.events", facts.MessagingOperationPublish, "publishKafka", "kafka.yaml")
	kafkaContract.Props[facts.PropMessaging] = "kafka-secure"
	mqttContract := contract("shared.events", facts.MessagingOperationPublish, "publishMQTT", "mqtt.yaml")
	mqttContract.Props[facts.PropMessaging] = "mqtt"
	codeFact := code("shared.events", facts.MessagingOperationPublish, "events.Publish", 12)
	codeFact.Props[facts.PropMessaging] = "kafka"
	store := facts.NewStore()
	store.Add(kafkaContract, mqttContract, codeFact)
	if err := New().Bind(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	for _, f := range store.FactsRef() {
		if isCodeOperation(f) {
			if f.Props[facts.PropMessagingContractBound] != true || f.PropString(facts.PropMessagingContractOperationID) != "publishKafka" {
				t.Fatalf("Kafka call binding = %+v", f.Props)
			}
		}
		if isContractOperation(f) && f.PropString("operationId") == "publishMQTT" && f.Props[facts.PropMessagingImplementationCount] != 0 {
			t.Fatalf("MQTT contract received Kafka implementation: %+v", f)
		}
	}
}
