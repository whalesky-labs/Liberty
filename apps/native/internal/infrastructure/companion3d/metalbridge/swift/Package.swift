// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Metalbridge",
    platforms: [.macOS(.v13)],
    products: [.library(name: "Metalbridge", type: .dynamic, targets: ["Metalbridge"])],
    dependencies: [],
    targets: [
        .target(name: "Metalbridge"),
    ],
    swiftLanguageModes: [.v5]
)
