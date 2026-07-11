import Foundation

// DataModel is a base class declaring runRequest().
class DataModel {
    func runRequest() {
        print("request")
    }
}

// UserModel calls the inherited runRequest(). The bare call leaves a dangling
// `runRequest` edge that resolveInheritedCalls rewrites to the declaring ancestor
// DataModel.runRequest by walking the supertype chain (cache.go v48).
//
// runRequest is also declared `override` here, so the symbol carries override:true
// (cache.go v107) — a polymorphic override the framework may dispatch, excluded from
// dead-code orphan reporting.
final class UserModel: DataModel {
    override func runRequest() {
        print("user request")
    }

    func refresh() {
        runRequest()
    }
}
