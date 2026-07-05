import Foundation

enum HTTPMethod {
    case get
    case post
}

// APIEndpoint is a Moya/TargetType-style endpoint protocol. Its protocol-extension
// default `urlPrefixComponent` supplies the repo-wide version prefix "v2"
// (cache.go v70: default-prefix resolution), and a conforming endpoint enum is
// recognised as an HTTP client (cache.go v69: Swift endpoint-enum client).
protocol APIEndpoint {
    var urlPrefixComponent: String { get }
    var urlPathComponent: String { get }
    var method: HTTPMethod { get }
}

extension APIEndpoint {
    var urlPrefixComponent: String {
        "v2" // repo-wide default prefix for all endpoints
    }
}

// ReportsEndpoint declares no urlPrefixComponent of its own, so every case inherits
// the default "v2" prefix -> client routes "v2/reports.json" (GET and POST).
enum ReportsEndpoint: APIEndpoint {
    case list
    case send([String: Any])
}

extension ReportsEndpoint {
    var urlPathComponent: String {
        switch self {
        case .list: return "reports.json"
        case .send: return "reports.json"
        }
    }

    var method: HTTPMethod {
        switch self {
        case .list: return .get
        case .send: return .post
        }
    }
}
