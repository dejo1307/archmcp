import Foundation

final class Node {
    var parent: Node?
}

final class TreeWalker {
    // Genuine recursion: the self-call's argument label (`from:`) matches the
    // function's parameter label, so recursive_self IS set (cache.go v46).
    func walk(from node: Node) -> Node {
        if let parent = node.parent {
            return walk(from: parent)
        }
        return node
    }

    // Subscript collision: `parameters[...]` is a subscript on a same-named local,
    // NOT a call to func parameters() -> must not set recursive_self nor emit a
    // self-call edge (cache.go v45).
    func parameters() -> [String: Any] {
        var parameters: [String: Any] = [:]
        parameters["key"] = 1
        return parameters
    }
}

final class SelectableCell {
    // Override delegating to super with matching argument labels: shares the bare
    // name setSelected but is NOT self-recursion (cache.go v46).
    override func setSelected(_ selected: Bool, animated: Bool) {
        super.setSelected(selected, animated: animated)
    }
}
