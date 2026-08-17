package messagingcoverage

import (
	"context"
	"strings"
	"testing"

	"github.com/enola-labs/enola/internal/facts"
)

func TestExplainMessagingCoverage(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindStorage, Name: "orders.undeclared", File: "events.ts", Repo: "orders", Props: map[string]any{
			facts.PropMessagingContractStatus: facts.MessagingContractStatusUndeclared,
			facts.PropMessagingOperation:      facts.MessagingOperationPublish, "code_symbol": "src.publishOrder",
		}},
		facts.Fact{Kind: facts.KindStorage, Name: "orders.undeclared", File: "events.ts", Line: 42, Repo: "orders", Props: map[string]any{
			facts.PropMessagingContractStatus: facts.MessagingContractStatusUndeclared,
			facts.PropMessagingOperation:      facts.MessagingOperationPublish, "code_symbol": "src.publishOrder",
		}},
		// A repeated identical call-site fact should not duplicate evidence.
		facts.Fact{Kind: facts.KindStorage, Name: "orders.undeclared", File: "events.ts", Repo: "orders", Props: map[string]any{
			facts.PropMessagingContractStatus: facts.MessagingContractStatusUndeclared,
			facts.PropMessagingOperation:      facts.MessagingOperationPublish, "code_symbol": "src.publishOrder",
		}},
		facts.Fact{Kind: facts.KindStorage, Name: "orders.unimplemented", File: "asyncapi.yaml", Repo: "orders", Props: map[string]any{
			facts.PropMessagingImplementationStatus: facts.MessagingImplementationUnimplemented,
			facts.PropMessagingOperation:            facts.MessagingOperationSubscribe, "operationId": "receiveOrder",
		}},
		facts.Fact{Kind: facts.KindStorage, Name: "orders.bound", File: "asyncapi.yaml", Repo: "orders", Props: map[string]any{
			facts.PropMessagingImplementationStatus: facts.MessagingImplementationImplemented,
		}},
	)
	got, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d insights: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Title, "Undeclared messaging operations") || got[0].Confidence != 0.9 {
		t.Fatalf("undeclared insight = %+v", got[0])
	}
	if len(got[0].Evidence) != 2 || got[0].Evidence[0].Symbol != "src.publishOrder" {
		t.Fatalf("undeclared evidence = %+v", got[0].Evidence)
	}
	if !strings.Contains(got[1].Title, "Unimplemented messaging contract candidates") || got[1].Confidence != 0.65 {
		t.Fatalf("unimplemented insight = %+v", got[1])
	}
}

func TestExplainFlagsConflictingContracts(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindStorage, Name: "orders.created", File: "old.yaml", Repo: "orders", Props: map[string]any{
			facts.PropMessagingOperation:            facts.MessagingOperationPublish,
			facts.PropMessagingDuplicateOf:          []string{"new.yaml"},
			facts.PropMessagingImplementationStatus: facts.MessagingImplementationImplemented,
		}},
		facts.Fact{Kind: facts.KindStorage, Name: "orders.created", File: "new.yaml", Repo: "orders", Props: map[string]any{
			facts.PropMessagingOperation:            facts.MessagingOperationPublish,
			facts.PropMessagingDuplicateOf:          []string{"old.yaml"},
			facts.PropMessagingImplementationStatus: facts.MessagingImplementationImplemented,
		}},
	)
	got, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Title, "Conflicting messaging contracts") {
		t.Fatalf("got %+v", got)
	}
	if len(got[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v", got[0].Evidence)
	}
}

func TestExplainCountsOnlyCanonicalUnimplementedContract(t *testing.T) {
	store := facts.NewStore()
	store.Add(
		facts.Fact{Kind: facts.KindStorage, Name: "orders.created", File: "asyncapi.yaml", Repo: "orders", Props: map[string]any{
			facts.PropMessagingOperation: facts.MessagingOperationPublish, facts.PropMessagingImplementationStatus: facts.MessagingImplementationUnimplemented,
		}},
		facts.Fact{Kind: facts.KindStorage, Name: "orders.created", File: "bundle/asyncapi.yaml", Repo: "orders", Props: map[string]any{
			facts.PropMessagingOperation: facts.MessagingOperationPublish, facts.PropMessagingImplementationStatus: facts.MessagingImplementationUnimplemented,
			facts.PropMessagingCanonicalFile: "asyncapi.yaml",
		}},
	)
	got, err := New().Explain(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Evidence) != 1 || got[0].Evidence[0].File != "asyncapi.yaml" {
		t.Fatalf("got %+v", got)
	}
}

func TestExplainCleanStoreProducesNothing(t *testing.T) {
	got, err := New().Explain(context.Background(), facts.NewStore())
	if err != nil || len(got) != 0 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
