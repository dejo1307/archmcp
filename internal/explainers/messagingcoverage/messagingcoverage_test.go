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

func TestExplainCleanStoreProducesNothing(t *testing.T) {
	got, err := New().Explain(context.Background(), facts.NewStore())
	if err != nil || len(got) != 0 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
