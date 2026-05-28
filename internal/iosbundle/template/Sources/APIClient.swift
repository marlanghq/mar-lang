// Thin URLSession wrapper. Two responsibilities only:
//   - GET /_mar/program.json → raw bytes (the AST is decoded off-actor
//     by MarJSONCodec).
//   - POST any path with a JSON body → raw response bytes (the caller
//     decides what to do with them, since service responses are
//     untyped from the iOS side until we ship a type-aware codegen).

import Foundation

enum APIError: LocalizedError {
    case invalidURL
    case http(Int, String)
    case decoding(Error)
    case network(Error)

    var errorDescription: String? {
        switch self {
        case .invalidURL:           return "Invalid base URL — verify ios.serverUrl in mar.json."
        case .http(let code, let body):
            return "HTTP \(code): \(body.isEmpty ? "(empty body)" : body)"
        case .decoding(let err):    return "Decoding failed: \(err.localizedDescription)"
        case .network(let err):     return "Network error: \(err.localizedDescription)"
        }
    }
}

actor APIClient {
    private var baseURL: URL

    init(baseURL: URL) {
        self.baseURL = baseURL
    }

    func setBaseURL(_ url: URL) {
        baseURL = url
    }

    /// Raw program.json bytes — passed to MarJSONCodec.decodeProgram
    /// off-thread on the main actor.
    func fetchProgram() async throws -> Data {
        try await get(path: "/_mar/program.json")
    }

    /// POST a raw JSON body to `path`, return the raw response body.
    /// The caller parses or echoes it as a string — services have
    /// arbitrary response shapes so we don't impose a decoder here.
    func postJSON(path: String, body: String) async throws -> String {
        guard let url = URL(string: path, relativeTo: baseURL) else {
            throw APIError.invalidURL
        }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = body.data(using: .utf8)
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            let bodyText = String(data: data, encoding: .utf8) ?? ""
            if let http = resp as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
                throw APIError.http(http.statusCode, bodyText)
            }
            return bodyText
        } catch let err as APIError {
            throw err
        } catch {
            throw APIError.network(error)
        }
    }

    private func get(path: String) async throws -> Data {
        guard let url = URL(string: path, relativeTo: baseURL) else {
            throw APIError.invalidURL
        }
        do {
            let (data, resp) = try await URLSession.shared.data(from: url)
            if let http = resp as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
                let body = String(data: data, encoding: .utf8) ?? ""
                throw APIError.http(http.statusCode, body)
            }
            return data
        } catch let err as APIError {
            throw err
        } catch {
            throw APIError.network(error)
        }
    }
}
