// Package facts re-exports enola's internal fact model as public type aliases and
// constants so out-of-module code (e.g. enola-enterprise) can implement
// plugin.Explainer — whose method signatures name these types — and read facts
// without importing the internal package directly.
//
// These are Go type ALIASES, not new types: facts.Store here is the exact same
// type as the internal facts.Store, so a method written against the alias
// satisfies an interface (plugin.Explainer) declared against the original.
package facts

import internal "github.com/enola-labs/enola/internal/facts"

// Core model types (aliases — identical to the internal types).
type (
	Fact     = internal.Fact
	Relation = internal.Relation
	Evidence = internal.Evidence
	Insight  = internal.Insight
	Store    = internal.Store
	Snapshot = internal.Snapshot
	Artifact = internal.Artifact
)

// Fact kind constants.
const (
	KindModule     = internal.KindModule
	KindSymbol     = internal.KindSymbol
	KindRoute      = internal.KindRoute
	KindStorage    = internal.KindStorage
	KindDependency = internal.KindDependency
	KindService    = internal.KindService
)

// Relation kind constants.
const (
	RelDeclares     = internal.RelDeclares
	RelImports      = internal.RelImports
	RelCalls        = internal.RelCalls
	RelImplements   = internal.RelImplements
	RelDependsOn    = internal.RelDependsOn
	RelInstantiates = internal.RelInstantiates
	RelInjects      = internal.RelInjects
	RelHasMethod    = internal.RelHasMethod
)

// Symbol kind property values.
const (
	SymbolFunc      = internal.SymbolFunc
	SymbolMethod    = internal.SymbolMethod
	SymbolStruct    = internal.SymbolStruct
	SymbolInterface = internal.SymbolInterface
	SymbolType      = internal.SymbolType
	SymbolClass     = internal.SymbolClass
	SymbolVariable  = internal.SymbolVariable
	SymbolConstant  = internal.SymbolConstant
	SymbolEnum      = internal.SymbolEnum
)
