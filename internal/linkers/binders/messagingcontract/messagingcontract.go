// Package messagingcontract binds broker call sites to AsyncAPI operations.
package messagingcontract

import (
	"context"
	"log"
	"sort"
	"strconv"

	"github.com/enola-labs/enola/internal/facts"
	"github.com/enola-labs/enola/pkg/plugin"
)

type Binder struct{}

func New() *Binder                        { return &Binder{} }
func (b *Binder) Name() string            { return "messaging-contract" }
func (b *Binder) Stage() plugin.BindStage { return plugin.StagePostLink }

type contractRef struct {
	identity, operationID, file string
}

type binding struct {
	contract contractRef
	symbol   string
}

func (b *Binder) Bind(_ context.Context, store *facts.Store) error {
	all := store.FactsRef()
	contracts := map[string][]contractRef{}
	for _, f := range all {
		if !isContractOperation(f) {
			continue
		}
		key := operationKey(f)
		contracts[key] = append(contracts[key], contractRef{
			identity: contractIdentity(f), operationID: f.PropString("operationId"), file: f.File,
		})
	}

	bindings := map[string]binding{}
	implementers := map[string]map[string]bool{}
	for _, f := range all {
		if !isCodeOperation(f) {
			continue
		}
		matches := contracts[operationKey(f)]
		if len(matches) != 1 {
			continue
		}
		symbol := f.PropString("code_symbol")
		bindings[codeIdentity(f)] = binding{contract: matches[0], symbol: symbol}
		if symbol != "" {
			if implementers[matches[0].identity] == nil {
				implementers[matches[0].identity] = map[string]bool{}
			}
			implementers[matches[0].identity][symbol] = true
		}
	}

	bound := 0
	store.UpdateWhere(func(f *facts.Fact) {
		if f.Kind != facts.KindStorage || f.Props == nil {
			return
		}
		if isCodeOperation(*f) {
			delete(f.Props, facts.PropMessagingContractBound)
			delete(f.Props, facts.PropMessagingContractOperationID)
			delete(f.Props, facts.PropMessagingContractFile)
			if match, ok := bindings[codeIdentity(*f)]; ok {
				f.Props[facts.PropMessagingContractBound] = true
				if match.contract.operationID != "" {
					f.Props[facts.PropMessagingContractOperationID] = match.contract.operationID
				}
				f.Props[facts.PropMessagingContractFile] = match.contract.file
				bound++
			}
			return
		}
		if !isContractOperation(*f) {
			return
		}
		delete(f.Props, facts.PropMessagingImplementationCount)
		delete(f.Props, facts.PropMessagingImplementedBy)
		kept := f.Relations[:0]
		for _, relation := range f.Relations {
			if relation.Kind != facts.RelImplementedBy {
				kept = append(kept, relation)
			}
		}
		f.Relations = kept
		set := implementers[contractIdentity(*f)]
		if len(set) == 0 {
			return
		}
		symbols := make([]string, 0, len(set))
		for symbol := range set {
			symbols = append(symbols, symbol)
		}
		sort.Strings(symbols)
		f.Props[facts.PropMessagingImplementationCount] = len(symbols)
		f.Props[facts.PropMessagingImplementedBy] = symbols
		for _, symbol := range symbols {
			f.Relations = append(f.Relations, facts.Relation{Kind: facts.RelImplementedBy, Target: symbol})
		}
	})
	if bound > 0 {
		log.Printf("[binder:messaging-contract] bound %d Kafka call site(s) to AsyncAPI operations", bound)
	}
	return nil
}

func isContractOperation(f facts.Fact) bool {
	return f.Kind == facts.KindStorage && f.PropString("storage_kind") == facts.StorageKindTopic &&
		f.PropString(facts.PropSource) == facts.MessagingSourceAsyncAPI &&
		f.PropString(facts.PropMessagingOperation) != ""
}

func isCodeOperation(f facts.Fact) bool {
	return f.Kind == facts.KindStorage && f.PropString("storage_kind") == facts.StorageKindTopic &&
		f.PropString(facts.PropSource) == facts.MessagingSourceGoKafkaCall &&
		f.PropString(facts.PropMessagingOperation) != ""
}

func operationKey(f facts.Fact) string {
	return f.Repo + "\x00" + f.Name + "\x00" + f.PropString(facts.PropMessagingOperation)
}

func contractIdentity(f facts.Fact) string {
	return operationKey(f) + "\x00" + f.File + "\x00" + f.PropString("operationId")
}

func codeIdentity(f facts.Fact) string {
	return operationKey(f) + "\x00" + f.File + "\x00" + strconv.Itoa(f.Line) + "\x00" + f.PropString("code_symbol")
}
