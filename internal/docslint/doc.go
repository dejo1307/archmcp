// Package docslint checks the prose against the code it describes.
//
// enola already ties several written artifacts to the thing they document:
// internal/cachecov refuses a cacheVersion bump with no covering test,
// pkg/command/help_consistency_test.go refuses a documented command nothing
// dispatches, and internal/facts/contract.go refuses a vocabulary literal the
// reading side never registered. Every one of those exists because the two halves
// had already drifted apart once, silently, with the build green throughout.
//
// The markdown was the last surface with no such tie, and it had drifted the same
// way. docs/EXPLAINERS.md described "fifteen explainers" and "sixteen explainers"
// three paragraphs apart; ARCHITECTURE.md advertised fourteen MCP tools over
// fifteen sections while the server registered seventeen; docs/INTENT.md titled a
// section "The eleven rule forms" above a vocabulary of thirteen. Two links pointed
// at files that had been renamed, and one pointed at a heading whose meaning had
// since inverted. None of it failed anything.
//
// The checks here are deliberately mechanical. They assert that a number in the
// prose equals a number derived from the code, that a path in the prose exists, and
// that a page carries the sections its own index promises. They say nothing about
// whether the surrounding sentence is TRUE — a doc can describe an explainer
// perfectly wrongly and pass. What they remove is the class of error nobody catches
// by reading: the count that was right when it was typed.
//
// Two design choices are load-bearing.
//
// The inventories are DERIVED, never listed here. A count claim resolves through
// config.KnownExplainers, cli.OSSTools(), intent.RuleForms and layers.TaxonomyNames()
// — the same values the running binary uses. A second hand-maintained list in this
// package would be one more thing to drift.
//
// A frozen number is WAIVED BY PHRASE, with a reason, and a stale waiver fails.
// Some numbers in the docs are historical — a benchmark measured at a named version
// is not wrong because the code moved on — and some sentences use a number that is
// not a claim about an inventory at all. Both are legitimate, and both have to be
// stated rather than assumed, so the waiver table below is the complete list of
// numbers this repository has decided not to track. When the sentence around one is
// rewritten, its waiver stops matching and the test says so.
//
// Cost: no engine, no bootstrap, no tree-sitter, no CGO. It runs in milliseconds,
// which is what makes it cheap enough for .githooks/pre-push as well as CI.
package docslint
