import Foundation

// AliasTarget is reached only through the AliasName typealias, so the alias must
// credit it or the dead-code detector reports it as an unreferenced orphan
// (GAP-SW-09, cache.go v102). handleTypeAlias folds the aliased type in as an
// instantiation edge on the alias fact.
class AliasTarget {
    func perform() {}
}

typealias AliasName = AliasTarget

func useAlias() {
    let t = AliasName()
    t.perform()
}
