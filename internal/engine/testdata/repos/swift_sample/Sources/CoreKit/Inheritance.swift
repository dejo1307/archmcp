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
final class UserModel: DataModel {
    func refresh() {
        runRequest()
    }
}
