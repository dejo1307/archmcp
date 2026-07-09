// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "GolfJournal",
    products: [
        .library(name: "Core", targets: ["Core"])
    ],
    targets: [
        .target(name: "Core")
    ]
)
