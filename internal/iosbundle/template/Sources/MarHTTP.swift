// Async HTTP for the runtime — fire-and-dispatch semantics. Mirrors
// the JS pattern in runtime.js where `fetch().then(toMsg → dispatch)`
// is the universal shape for Service.call / Endpoint.call /
// Http.get / Http.post.
//
// The Effect itself returns synchronously; the response (Ok body /
// Err message) is wrapped in a Result and turned into a Msg by the
// user-supplied `toMsg` function, which is then dispatched into the
// running page's update loop.

import Foundation

enum MarHTTP {

    /// Generic HTTP fire (used by Endpoint.call + Http.get/post).
    /// `url` is treated as absolute; the caller is responsible for
    /// concatenation. `body` is sent as-is (already JSON-encoded by
    /// caller); when nil the request goes out without a body.
    static func fire(method: String, url: String, body: String?, toMsg: MarValue) {
        guard let u = URL(string: url) else {
            dispatchErr(toMsg, message: "invalid URL: \(url)")
            return
        }
        send(method: method, url: u, body: body, toMsg: toMsg)
    }

    /// Service.call variant — `path` is relative to the discovered /
    /// configured baseURL (resolved by MarDispatcher). Body is the
    /// JSON-encoded request payload. Response is parsed as JSON
    /// before being wrapped in `Ok` so user code gets a typed value
    /// back instead of a raw string.
    static func fireService(path: String, body: String, toMsg: MarValue) {
        Task { @MainActor in
            guard let url = MarDispatcher.shared.resolve(path: path) else {
                dispatchErr(toMsg, message: "invalid service path: \(path)")
                return
            }
            sendService(url: url, body: body, toMsg: toMsg)
        }
    }

    private static func send(method: String, url: URL, body: String?, toMsg: MarValue) {
        var req = URLRequest(url: url)
        req.httpMethod = method
        if let body, method != "GET", method != "DELETE" {
            req.httpBody = body.data(using: .utf8)
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        URLSession.shared.dataTask(with: req) { data, response, error in
            let result = makeStringResult(data: data, response: response, error: error)
            Task { @MainActor in
                deliverMsg(result: result, toMsg: toMsg)
            }
        }.resume()
    }

    private static func sendService(url: URL, body: String, toMsg: MarValue) {
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.httpBody = body.data(using: .utf8)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        URLSession.shared.dataTask(with: req) { data, response, error in
            // Auth-expiry interceptor: 401 from a Service.call means
            // the session is gone. Route to signInPath instead of
            // surfacing the Err to user code. Mirrors the JS runtime's
            // handleAuthExpired so app code looks identical on both
            // platforms (no per-update auth-checks anywhere).
            let status = (response as? HTTPURLResponse)?.statusCode ?? 0
            if status == 401 {
                Task { @MainActor in
                    if AppContext.shared.handleAuthExpired() {
                        return
                    }
                    // No signInPath configured — fall through and
                    // surface the Err normally so the app at least
                    // sees something.
                    let r = makeJSONResult(data: data, response: response, error: error)
                    deliverMsg(result: r, toMsg: toMsg)
                }
                return
            }
            let r = makeJSONResult(data: data, response: response, error: error)
            Task { @MainActor in
                deliverMsg(result: r, toMsg: toMsg)
            }
        }.resume()
    }

    /// Result decoder for the auth endpoints. ackUnit = "200 OK means
    /// success, ignore body" (used by request-code, logout). userJSON =
    /// "decode the body as a Mar value" (used by verify-code where the
    /// body is the User record).
    enum AuthDecode {
        case ackUnit
        case userJSON
    }

    /// Fires a POST to /_auth/<endpoint> with the runtime-encoded body.
    /// On 200, dispatches Ok (with the decoded value or unit). On
    /// non-2xx, dispatches Err with the server's error message.
    /// Cookies are persisted by URLSession.shared automatically across
    /// app launches via HTTPCookieStorage.shared.
    static func fireAuth(path: String, body: MarValue?, decode: AuthDecode, toMsg: MarValue) {
        Task { @MainActor in
            guard let url = MarDispatcher.shared.resolve(path: path) else {
                dispatchErr(toMsg, message: "invalid auth path: \(path)")
                return
            }
            var req = URLRequest(url: url)
            req.httpMethod = "POST"
            if let body {
                let bodyAny = MarJSONCodec.marToJSON(body)
                let bodyData = try? JSONSerialization.data(
                    withJSONObject: bodyAny,
                    options: [.fragmentsAllowed]
                )
                req.httpBody = bodyData
                req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            }
            URLSession.shared.dataTask(with: req) { data, response, error in
                let outcome = decodeAuthResponse(
                    data: data, response: response, error: error, decode: decode
                )
                Task { @MainActor in
                    deliverMsg(result: outcome, toMsg: toMsg)
                }
            }.resume()
        }
    }

    /// GET /_auth/me as an async function (no dispatcher dependency).
    /// Used by the gating layer (LoadedShell) to bootstrap auth before
    /// any page is mounted. Returns:
    ///   .ctor("Just", [user]) — logged in
    ///   .ctor("Nothing", [])  — not logged in
    ///   nil                   — network error (caller decides how to surface)
    static func fetchAuthMe() async -> MarValue? {
        guard let url = await MainActor.run(body: { MarDispatcher.shared.resolve(path: "/_auth/me") })
        else { return nil }
        do {
            let (data, response) = try await URLSession.shared.data(for: URLRequest(url: url))
            if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
                return nil
            }
            let any = (try? JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed]))
            if let any, !(any is NSNull) {
                let user = MarJSONCodec.jsonToMar(any)
                return .ctor(tag: "Just", args: [user], origin: nil)
            }
            return .ctor(tag: "Nothing", args: [], origin: nil)
        } catch {
            return nil
        }
    }

    /// GET /_auth/me. Body is either a JSON user record or `null`.
    /// Resolves to `Ok (Just user)` or `Ok Nothing`. Network errors
    /// resolve to `Err message`.
    static func fireAuthMe(toMsg: MarValue) {
        Task { @MainActor in
            guard let url = MarDispatcher.shared.resolve(path: "/_auth/me") else {
                dispatchErr(toMsg, message: "invalid auth path: /_auth/me")
                return
            }
            URLSession.shared.dataTask(with: URLRequest(url: url)) { data, response, error in
                let outcome: FetchOutcome
                if let error {
                    outcome = .err(error.localizedDescription)
                } else if let http = response as? HTTPURLResponse,
                          !(200..<300).contains(http.statusCode) {
                    let body = data.flatMap { String(data: $0, encoding: .utf8) } ?? ""
                    outcome = .err(body.isEmpty ? "HTTP \(http.statusCode)" : body)
                } else {
                    let raw = data ?? Data()
                    let any = (try? JSONSerialization.jsonObject(
                        with: raw, options: [.fragmentsAllowed])
                    )
                    if let any, !(any is NSNull) {
                        let user = MarJSONCodec.jsonToMar(any)
                        outcome = .okJSON(.ctor(tag: "Just", args: [user], origin: nil))
                    } else {
                        outcome = .okJSON(.ctor(tag: "Nothing", args: [], origin: nil))
                    }
                }
                Task { @MainActor in
                    deliverMsg(result: outcome, toMsg: toMsg)
                }
            }.resume()
        }
    }

    private static func decodeAuthResponse(
        data: Data?, response: URLResponse?, error: Error?, decode: AuthDecode
    ) -> FetchOutcome {
        if let error { return .err(error.localizedDescription) }
        let raw = data ?? Data()
        let body = String(data: raw, encoding: .utf8) ?? ""
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            // Server returns {"error": "code"} on auth failures.
            if let any = try? JSONSerialization.jsonObject(with: raw, options: []) as? [String: Any],
               let code = any["error"] as? String {
                return .err(code)
            }
            return .err(body.isEmpty ? "HTTP \(http.statusCode)" : body)
        }
        switch decode {
        case .ackUnit:
            return .okJSON(.unit)
        case .userJSON:
            do {
                let any = try JSONSerialization.jsonObject(with: raw, options: [.fragmentsAllowed])
                return .okJSON(MarJSONCodec.jsonToMar(any))
            } catch {
                return .err("decode failed: \(error.localizedDescription)")
            }
        }
    }

    // MARK: - Result builders

    private enum FetchOutcome {
        case ok(String)
        case okJSON(MarValue)
        case err(String)
    }

    private static func makeStringResult(data: Data?, response: URLResponse?, error: Error?) -> FetchOutcome {
        if let error { return .err(error.localizedDescription) }
        let body = data.flatMap { String(data: $0, encoding: .utf8) } ?? ""
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            return .err(body.isEmpty ? "HTTP \(http.statusCode)" : body)
        }
        return .ok(body)
    }

    private static func makeJSONResult(data: Data?, response: URLResponse?, error: Error?) -> FetchOutcome {
        if let error { return .err(error.localizedDescription) }
        let bodyData = data ?? Data()
        let bodyString = String(data: bodyData, encoding: .utf8) ?? ""
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            return .err(bodyString.isEmpty ? "HTTP \(http.statusCode)" : bodyString)
        }
        do {
            let any = try JSONSerialization.jsonObject(with: bodyData, options: [.fragmentsAllowed])
            return .okJSON(MarJSONCodec.jsonToMar(any))
        } catch {
            return .err("decode failed: \(error.localizedDescription)")
        }
    }

    @MainActor
    private static func deliverMsg(result: FetchOutcome, toMsg: MarValue) {
        let resultValue: MarValue
        switch result {
        case .ok(let s):
            resultValue = .ctor(tag: "Ok", args: [.string(s)], origin: nil)
        case .okJSON(let v):
            resultValue = .ctor(tag: "Ok", args: [v], origin: nil)
        case .err(let m):
            resultValue = .ctor(tag: "Err", args: [.string(m)], origin: nil)
        }
        do {
            let msg = try Eval.apply(toMsg, resultValue)
            MarDispatcher.shared.dispatch(msg)
        } catch {
            // toMsg failed to apply — surface it as a console
            // diagnostic. There's nowhere good to dispatch this since
            // the failure is in the message-construction itself.
            print("[mar] toMsg failed: \(error.localizedDescription)")
        }
    }

    private static func dispatchErr(_ toMsg: MarValue, message: String) {
        Task { @MainActor in
            do {
                let msg = try Eval.apply(toMsg, .ctor(tag: "Err", args: [.string(message)], origin: nil))
                MarDispatcher.shared.dispatch(msg)
            } catch {
                print("[mar] error dispatch failed: \(error.localizedDescription)")
            }
        }
    }
}
