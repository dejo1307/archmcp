import Foundation

// Gate is declared once in each branch of a #if/#else conditional-compilation
// block. Tree-sitter walks BOTH branches (it does not evaluate os(macOS)), so the
// type yields two same-name symbol facts differing only in line — one declaration
// in any single build. Each carries conditional=true so consumers can group/dedupe
// them (GAP-SW-10, cache.go v108). runFull / runSkipped are the per-branch members,
// also conditional.
#if os(macOS)
final class Gate {
    func runFull() {}
}
#else
final class Gate {
    func runSkipped() {}
}
#endif
