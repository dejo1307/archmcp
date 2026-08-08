// Package grammar vendors the tree-sitter Dart grammar (UserNobody14/tree-sitter-dart).
//
// The upstream Go binding cannot be consumed directly for two independent reasons,
// either of which alone is fatal:
//
//  1. It binds github.com/smacker/go-tree-sitter, a DIFFERENT runtime than the
//     github.com/tree-sitter/go-tree-sitter enola uses.
//  2. Its committed src/parser.c is generated against tree-sitter ABI 15, while the
//     vendored runtime accepts at most 14. That rejection is SILENT — SetLanguage
//     fails, every file parses to nothing, and the result is indistinguishable from a
//     repository containing no Dart. This is the same trap documented for the C# and
//     Scala grammars.
//
// So the parser is regenerated from the upstream grammar.json at ABI 14 and committed
// under src/, alongside the hand-written scanner.c copied verbatim. Regenerate with:
//
//	cd grammar && tree-sitter generate grammar.json --abi 14
//
// The generated parser (src/parser.c) and the external scanner (src/scanner.c) are
// compiled as separate translation units via the cgo_*.c shims in this directory, NOT
// #include'd together here: both define grammar-local macros (e.g. TOKEN_COUNT), so
// combining them into one translation unit triggers -Wmacro-redefined.
package grammar

// #cgo CFLAGS: -std=c11 -fPIC -I${SRCDIR}/src
// #include "tree_sitter/parser.h"
// const TSLanguage *tree_sitter_dart(void);
import "C"

import "unsafe"

// Language returns the tree-sitter Language pointer for Dart, suitable for
// sitter.NewLanguage(...).
func Language() unsafe.Pointer {
	return unsafe.Pointer(C.tree_sitter_dart())
}
