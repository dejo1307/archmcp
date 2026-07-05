import XCTest
@testable import CoreKit

// This file belongs to the CoreKitTests testTarget declared in Package.swift, so
// its module fact carries module_role=test (cache.go v49), distinguishing the test
// population from the production CoreKit target.
final class CoreKitTests: XCTestCase {
    func testNodeHasNoParentByDefault() {
        let node = Node()
        XCTAssertNil(node.parent)
    }
}
