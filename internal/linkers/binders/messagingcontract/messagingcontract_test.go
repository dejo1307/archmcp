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
		if isCodeOperation(f) && f.Props[facts.PropMessagingContractBound] == true {
			t.Errorf("ambiguous/unmatched code operation was bound: %+v", f)
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
