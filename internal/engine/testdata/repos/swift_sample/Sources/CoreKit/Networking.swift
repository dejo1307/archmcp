import Foundation

// FeedService exercises the Swift I/O heuristics (cache.go v47):
//   - fetchFeed() invokes a direct network primitive (URLSession.shared.data(for:))
//     -> io_direct=true, and also yields a URLSession client route (v2 detection).
//   - loadFeed() calls fetchFeed() but does no network itself -> performs_io=true
//     is computed transitively up the call graph.
final class FeedService {
    let baseURL: URL

    init(baseURL: URL) {
        self.baseURL = baseURL
    }

    // Direct network I/O primitive -> io_direct.
    func fetchFeed() async throws -> Data {
        var request = URLRequest(url: baseURL.appendingPathComponent("feed/items"))
        request.httpMethod = "GET"
        let (data, _) = try await URLSession.shared.data(for: request)
        return data
    }

    // No I/O of its own; reaches the network only through fetchFeed() -> performs_io.
    func loadFeed() async throws -> Data {
        return try await fetchFeed()
    }
}
