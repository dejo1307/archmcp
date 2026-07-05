// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "CoreKit",
    products: [
        .library(name: "CoreKit", targets: ["CoreKit"])
    ],
    targets: [
        .target(name: "CoreKit"),
        .testTarget(name: "CoreKitTests", dependencies: ["CoreKit"], path: "Tests/CoreKitTests")
    ]
)
