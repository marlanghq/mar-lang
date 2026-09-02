// Async HTTP for the runtime: fire-and-dispatch semantics. Mirrors
// the JS pattern in runtime.js where `fetch().then(toMsg → dispatch)`
// is the universal shape for Service.call / Http.get / Http.post.
//
// The Effect itself returns synchronously; the response (Ok body /
// Err message) is wrapped in a Result and turned into a Msg by the
// user-supplied `toMsg` function, which is then dispatched into the
// running page's update loop.

import Foundation

enum MarHTTP {

    /// Response header carrying the freshly-minted session token on a
    /// successful POST /_auth/verify-code. Mirror of the server-side
    /// `bearerTokenHeader` constant in internal/jsserve/auth.go. The
    /// runtime grabs it, stashes the value in Keychain, and attaches
    /// it as `Authorization: Bearer …` on subsequent requests.
    static let bearerTokenHeader = "X-Mar-Auth-Token"

    /// Attach the stored bearer to `req` if Keychain has one. No-op
    /// when there's no stored token: that's the pre-login state, the
    /// request goes out anonymous, and the server returns 401 if the
    /// route required auth. Called from every request site so the
    /// runtime never forgets a credential.
    private static func attachBearer(_ req: inout URLRequest) {
        if let tok = MarKeychain.load(forKey: MarKeychain.sessionTokenKey) {
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
        }
    }

    /// Wipe the local credential. Called on Auth.logout and any time
    /// the server tells us the session is invalid (401 on a Service
    /// call, /_auth/me returning null). Idempotent: Keychain delete
    /// of a missing key is a no-op.
    @MainActor
    static func clearStoredToken() {
        MarKeychain.delete(forKey: MarKeychain.sessionTokenKey)
    }

    /// Generic HTTP fire (used by Http.get / Http.post). `url` is treated
    /// as absolute; the caller is responsible for concatenation. `body` is
    /// sent as-is (already JSON-encoded by caller); when nil the request
    /// goes out without a body.
    static func fire(method: String, url: String, body: String?, toMsg: MarValue) {
        guard let u = URL(string: url) else {
            dispatchErr(toMsg, message: "invalid URL: \(url)")
            return
        }
        send(method: method, url: u, body: body, toMsg: toMsg)
    }

    /// Service.call variant: `method` is the contract's HTTP verb and
    /// `path` is the request URL (already built by buildServiceRequest:
    /// typed path params substituted in, the rest in the `q` query param
    /// for GET / DELETE). `path` is relative to the discovered /
    /// configured baseURL (resolved by MarDispatcher). `body` is the
    /// JSON-encoded payload for POST / PUT / PATCH, or nil when there's
    /// nothing to send. Response is parsed as JSON before being wrapped
    /// in `Ok` so user code gets a typed value back instead of a raw
    /// string.
    static func fireService(method: String, path: String, body: String?, toMsg: MarValue, responseShape: MarWireShape? = nil) {
        Task { @MainActor in
            guard let url = MarDispatcher.shared.resolve(path: path) else {
                dispatchServiceErr(toMsg, message: "invalid service path: \(path)")
                return
            }
            sendService(method: method, url: url, body: body, toMsg: toMsg, responseShape: responseShape)
        }
    }

    private static func send(method: String, url: URL, body: String?, toMsg: MarValue) {
        var req = URLRequest(url: url)
        req.httpMethod = method
        if let body, method != "GET", method != "DELETE" {
            req.httpBody = body.data(using: .utf8)
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        attachBearer(&req)
        URLSession.shared.dataTask(with: req) { data, response, error in
            let result = makeStringResult(data: data, response: response, error: error)
            Task { @MainActor in
                deliverMsg(result: result, toMsg: toMsg)
            }
        }.resume()
    }

    private static func sendService(method: String, url: URL, body: String?, toMsg: MarValue, responseShape: MarWireShape?) {
        var req = URLRequest(url: url)
        req.httpMethod = method
        if let body {
            req.httpBody = body.data(using: .utf8)
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        attachBearer(&req)
        URLSession.shared.dataTask(with: req) { data, response, error in
            // Auth-expiry interceptor: 401 from a Service.call means
            // the session is gone. Route to signInPath instead of
            // surfacing the Err to user code. Mirrors the JS runtime's
            // handleAuthExpired so app code looks identical on both
            // platforms (no per-update auth-checks anywhere).
            let status = (response as? HTTPURLResponse)?.statusCode ?? 0
            if status == 401 {
                Task { @MainActor in
                    // Token's no good: wipe it so the sign-in screen
                    // doesn't keep re-trying the same dead credential
                    // and so the next cold start lands on sign-in
                    // cleanly instead of bouncing through a 401.
                    clearStoredToken()
                    if AppContext.shared.handleAuthExpired() {
                        return
                    }
                    // No signInPath configured: fall through and
                    // surface Err Unauthorized so the app at least
                    // sees something.
                    let r = makeServiceResult(data: data, response: response, error: error, responseShape: responseShape)
                    deliverServiceResult(r, toMsg: toMsg)
                }
                return
            }
            let r = makeServiceResult(data: data, response: response, error: error, responseShape: responseShape)
            Task { @MainActor in
                deliverServiceResult(r, toMsg: toMsg)
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
    ///
    /// Sessions are credentialed via `Authorization: Bearer …` from
    /// the Keychain-backed token. URLSession.shared also still
    /// persists a Set-Cookie if the server happens to issue one, but
    /// the Bearer is the authoritative credential: server-side
    /// `extractSessionToken` returns the header value first, the
    /// cookie only as a fallback. That keeps the model uniform with
    /// future Android/Windows runtimes where cookie storage is
    /// unreliable or absent.
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
            attachBearer(&req)
            URLSession.shared.dataTask(with: req) { data, response, error in
                // Capture the token from /_auth/verify-code's
                // response before decoding. The server sets
                // `X-Mar-Auth-Token` on success; we copy it to
                // Keychain so subsequent requests carry the Bearer.
                // The header is absent on other auth endpoints
                // (request-code, logout): `headerToken` is nil
                // there and we skip the save.
                if let http = response as? HTTPURLResponse,
                   (200..<300).contains(http.statusCode),
                   let headerToken = http.value(forHTTPHeaderField: bearerTokenHeader),
                   !headerToken.isEmpty {
                    MarKeychain.save(headerToken, forKey: MarKeychain.sessionTokenKey)
                }
                let outcome = decodeAuthResponse(
                    data: data, response: response, error: error, decode: decode
                )
                Task { @MainActor in
                    // For user-returning auth endpoints (verify-code,
                    // and any future signUp/refresh that yields a User),
                    // park the freshly-authed user in AppContext BEFORE
                    // dispatching the Msg. Two reasons:
                    //
                    //   1. The mar update typically returns a
                    //      `Auth.completeSignIn` effect that lands on a
                    //      Page.protected. The protectedView gate
                    //      reads `currentUser` to decide whether to
                    //      render or bounce back to sign-in. Without
                    //      this capture, currentUser is nil → bounce
                    //      → user "loops" back to the email screen.
                    //
                    //   2. Avoids an immediate redundant Auth.me round
                    //      trip after sign-in, which is also racy
                    //      against the just-set session cookie.
                    if case .userJSON = decode,
                       case .okJSON(let userVal) = outcome {
                        AppContext.shared.currentUser =
                            .ctor(tag: "Just", args: [userVal], origin: nil)
                    }
                    deliverMsg(result: outcome, toMsg: toMsg)
                }
            }.resume()
        }
    }

    /// GET /_auth/me as an async function (no dispatcher dependency).
    /// Used by the gating layer (LoadedShell) to bootstrap auth before
    /// any page is mounted. Returns:
    ///   .ctor("Just", [user]): logged in
    ///   .ctor("Nothing", [])  - not logged in
    ///   nil                   - network error (caller decides how to surface)
    static func fetchAuthMe() async -> MarValue? {
        guard let url = await MainActor.run(body: { MarDispatcher.shared.resolve(path: "/_auth/me") })
        else { return nil }
        do {
            var req = URLRequest(url: url)
            attachBearer(&req)
            let (data, response) = try await URLSession.shared.data(for: req)
            if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
                if http.statusCode == 401 {
                    await MainActor.run { clearStoredToken() }
                }
                return nil
            }
            let any = (try? JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed]))
            // A body that will not decode is "no user", which is the
            // failure this function already has a shape for.
            if let any, !(any is NSNull), let user = try? MarJSONCodec.jsonToMar(any) {
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
    static func fireAuthMe(toMsg: MarValue, userShape: MarWireShape? = nil) {
        Task { @MainActor in
            guard let url = MarDispatcher.shared.resolve(path: "/_auth/me") else {
                dispatchErr(toMsg, message: "invalid auth path: /_auth/me")
                return
            }
            var req = URLRequest(url: url)
            attachBearer(&req)
            URLSession.shared.dataTask(with: req) { data, response, error in
                let outcome: FetchOutcome
                if let error {
                    outcome = .err(transportMessage(error))
                } else if let http = response as? HTTPURLResponse,
                          !(200..<300).contains(http.statusCode) {
                    let body = data.flatMap { String(data: $0, encoding: .utf8) } ?? ""
                    outcome = .err(body.isEmpty ? "HTTP \(http.statusCode)" : body)
                } else {
                    let raw = data ?? Data()
                    let any = (try? JSONSerialization.jsonObject(
                        with: raw, options: [.fragmentsAllowed])
                    )
                    // `null` is a signed-out answer. Anything else is supposed
                    // to be the app's own user, and used to become `Nothing`
                    // when it was not — so a server that had moved on made the
                    // phone report the person as signed out, silently, while
                    // the browser said the response did not match.
                    if any == nil {
                        outcome = .err(notJSONMessage)
                    } else if any is NSNull {
                        outcome = .okJSON(.ctor(tag: "Nothing", args: [], origin: nil))
                    } else if let user = try? MarJSONCodec.jsonToMar(any!) {
                        if let shape = userShape, let err = MarWire.check(user, shape) {
                            outcome = .err(wireMismatchMessage(err))
                        } else {
                            let checked = userShape.map { MarWire.coerce(user, $0) } ?? user
                            outcome = .okJSON(.ctor(tag: "Just", args: [checked], origin: nil))
                        }
                    } else {
                        outcome = .err(notJSONMessage)
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
        if let error { return .err(transportMessage(error)) }
        let raw = data ?? Data()
        let body = String(data: raw, encoding: .utf8) ?? ""
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            return .err(decodeServerError(body: raw, fallbackBody: body, status: http.statusCode))
        }
        switch decode {
        case .ackUnit:
            return .okJSON(.unit)
        case .userJSON:
            do {
                let any = try JSONSerialization.jsonObject(with: raw, options: [.fragmentsAllowed])
                return .okJSON(try MarJSONCodec.jsonToMar(any))
            } catch {
                return .err(notJSONMessage)
            }
        }
    }

    /// Extract a stable error code from an HTTP error response body.
    ///
    /// Mar's framework endpoints (auth, rate-limit middleware, admin)
    /// consistently shape error bodies as `{"error": "snake_case_code", ...}`.
    /// When that shape is present, return just the code so user code
    /// can `case` on it cleanly. Otherwise fall back to the raw body
    /// or, if even the body is empty, to "HTTP <status>".
    ///
    /// Mirrors the JS runtime's `decodeServerError`. Without this,
    /// a 429 from the gateway limiter would surface in a Result.Err
    /// as the literal JSON string `{"error":"rate_limited",...}`,
    /// defeating the point of having stable codes server-side.
    // Not private: internal/iosbundle/swift_core_test.go calls this directly.
    // The wording a failed call surfaces is the only thing a user sees when a
    // service is down, and it used to be compared on no runtime at all.
    static func decodeServerError(body data: Data, fallbackBody: String, status: Int) -> String {
        var decoded = fallbackBody
        if let any = try? JSONSerialization.jsonObject(with: data, options: []) as? [String: Any],
           let code = any["error"] as? String {
            // Even when the code is EMPTY. An empty code is no code, and the
            // envelope was still understood, so the raw JSON is not what to
            // show a user. Refusing empty here and falling back to the body
            // was how `{"error":""}` reached the phone as the literal
            // `{"error":""}` while the browser said `HTTP 500`.
            decoded = code
        }
        return decoded.isEmpty ? "HTTP \(status)" : decoded
    }

    // MARK: - Result builders

    private enum FetchOutcome {
        case ok(String)
        case okJSON(MarValue)
        case err(String)
    }

    private static func makeStringResult(data: Data?, response: URLResponse?, error: Error?) -> FetchOutcome {
        if let error { return .err(transportMessage(error)) }
        let raw = data ?? Data()
        let body = String(data: raw, encoding: .utf8) ?? ""
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            return .err(decodeServerError(body: raw, fallbackBody: body, status: http.statusCode))
        }
        return .ok(body)
    }

    private static func makeJSONResult(data: Data?, response: URLResponse?, error: Error?) -> FetchOutcome {
        if let error { return .err(transportMessage(error)) }
        let bodyData = data ?? Data()
        let bodyString = String(data: bodyData, encoding: .utf8) ?? ""
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            return .err(decodeServerError(body: bodyData, fallbackBody: bodyString, status: http.statusCode))
        }
        do {
            let any = try JSONSerialization.jsonObject(with: bodyData, options: [.fragmentsAllowed])
            return .okJSON(try MarJSONCodec.jsonToMar(any))
        } catch {
            return .err(notJSONMessage)
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
            // toMsg failed to apply: surface it as a console
            // diagnostic. There's nowhere good to dispatch this since
            // the failure is in the message-construction itself.
            print("[mar] toMsg failed: \(error.localizedDescription)")
        }
    }

    // MARK: Auth outcome boundary (Auth.RequestOutcome / Auth.VerifyOutcome)

    /// Drives Auth.requestCode / Auth.verifyCode: domain codes the endpoint
    /// is known to emit become typed outcome constructors in the Ok (the
    /// screen cases on them); everything else is transport and becomes a
    /// Service.Error in the Err, exactly like Service.call. Mirrors the JS
    /// runtime's authOutcomePost. Still captures the X-Mar-Auth-Token
    /// header on success so the Bearer credential lands in the Keychain.
    static func fireAuthOutcome(
        path: String,
        body: MarValue?,
        // @Sendable: these pure ctor-builders are captured by URLSession's
        // @Sendable completion handler below. The call sites pass literal
        // closures that only build ctors from their args (no shared state), so
        // the annotation is free and silences the strict-concurrency capture
        // warning.
        okOutcome: @escaping @Sendable (MarValue) -> MarValue,
        mapCode: @escaping @Sendable (String) -> MarValue?,
        toMsg: MarValue,
        userShape: MarWireShape? = nil
    ) {
        Task { @MainActor in
            guard let url = MarDispatcher.shared.resolve(path: path) else {
                dispatchServiceErr(toMsg, message: "invalid auth path: \(path)")
                return
            }
            var req = URLRequest(url: url)
            req.httpMethod = "POST"
            if let body {
                let bodyAny = MarJSONCodec.marToJSON(body)
                req.httpBody = try? JSONSerialization.data(
                    withJSONObject: bodyAny,
                    options: [.fragmentsAllowed]
                )
                req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            }
            attachBearer(&req)
            URLSession.shared.dataTask(with: req) { data, response, error in
                if let http = response as? HTTPURLResponse,
                   (200..<300).contains(http.statusCode),
                   let headerToken = http.value(forHTTPHeaderField: bearerTokenHeader),
                   !headerToken.isEmpty {
                    MarKeychain.save(headerToken, forKey: MarKeychain.sessionTokenKey)
                }
                let resultValue = makeAuthOutcomeResult(
                    data: data, response: response, error: error,
                    okOutcome: okOutcome, mapCode: mapCode, userShape: userShape
                )
                Task { @MainActor in
                    deliverServiceResult(resultValue, toMsg: toMsg)
                }
            }.resume()
        }
    }

    // Not private: internal/iosbundle drives it, so the auth port is compared
    // with the browser's without a real request.
    static func makeAuthOutcomeResult(
        data: Data?, response: URLResponse?, error: Error?,
        okOutcome: (MarValue) -> MarValue,
        mapCode: (String) -> MarValue?,
        userShape: MarWireShape? = nil
    ) -> MarValue {
        if error != nil {
            return .ctor(tag: "Err", args: [serviceErrorOffline()], origin: nil)
        }
        let bodyData = data ?? Data()
        let bodyString = String(data: bodyData, encoding: .utf8) ?? ""
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            let code = decodeServerError(body: bodyData, fallbackBody: bodyString, status: http.statusCode)
            if let domain = mapCode(code) {
                return .ctor(tag: "Ok", args: [domain], origin: nil)
            }
            let se = serviceErrorFromResponse(status: http.statusCode, body: bodyData, fallbackBody: bodyString)
            return .ctor(tag: "Err", args: [se], origin: nil)
        }
        // What the endpoint answered is supposed to be the app's own user.
        // Before the auth port had a shape, a body that would not parse became
        // `()` and `Auth.SignedIn ()` reached code typed as a user record; a
        // body of the WRONG shape reached it just as intact.
        guard let any = (try? JSONSerialization.jsonObject(with: bodyData, options: [.fragmentsAllowed])),
              let decoded = try? MarJSONCodec.jsonToMar(any) else {
            return .ctor(tag: "Err",
                         args: [.ctor(tag: "ServerError", args: [.string(notJSONMessage)], origin: nil)],
                         origin: nil)
        }
        if let shape = userShape, let err = MarWire.check(decoded, shape) {
            return .ctor(tag: "Err",
                         args: [.ctor(tag: "ServerError",
                                      args: [.string(wireMismatchMessage(err))], origin: nil)],
                         origin: nil)
        }
        let checked = userShape.map { MarWire.coerce(decoded, $0) } ?? decoded
        return .ctor(tag: "Ok", args: [okOutcome(checked)], origin: nil)
    }

    // MARK: Service.call result (Service.Error union)
    //
    // A Service.call delivers `Result Service.Error resp`: the Err is a
    // union (Offline / Unauthorized / RateLimited / ServerError String), not
    // a string, so transport failure is a value the app cases on. Mirrors
    // the JS runtime's serviceErrorFromResponse / serviceErrorOffline and the
    // Go serviceErrorString.

    static func serviceErrorOffline() -> MarValue {
        .ctor(tag: "Offline", args: [], origin: nil)
    }

    static func serviceErrorFromResponse(status: Int, body: Data, fallbackBody: String) -> MarValue {
        if status == 401 { return .ctor(tag: "Unauthorized", args: [], origin: nil) }
        if status == 429 { return .ctor(tag: "RateLimited", args: [], origin: nil) }
        let msg = decodeServerError(body: body, fallbackBody: fallbackBody, status: status)
        return .ctor(tag: "ServerError", args: [.string(msg)], origin: nil)
    }

    /// Builds the Result a Service.call delivers: Ok decoded-value, or Err
    /// carrying a Service.Error. The String variant (makeJSONResult) stays
    /// for Http.get/post and auth, whose Err is still a bare String.
    /// The sentence a TRANSPORT failure gets.
    ///
    /// Foundation's `localizedDescription` is the host's wording and it is
    /// LOCALIZED: the same dropped connection read one way on an English
    /// phone and another way on a Portuguese one, while the browser answered
    /// with one of two fixed sentences either way. These are those two
    /// sentences; friendlyFetchError in internal/jsserve/runtime.js is the
    /// other half, and internal/parityfixture holds them both.
    static func transportMessage(_ error: Error) -> String {
        // navigator.onLine === false is what the browser branches on, and
        // notConnectedToInternet is the same condition: no route at all,
        // rather than a request that started and failed.
        if (error as NSError).code == NSURLErrorNotConnectedToInternet {
            return "You appear to be offline. Check your connection and try again."
        }
        return "Couldn't reach the server. Try again in a moment."
    }

    /// The sentence a response of the wrong shape gets, the same one
    /// Service.call gives. Keep it identical to the browser's.
    static func wireMismatchMessage(_ detail: String) -> String {
        "the response does not match what this app expects: \(detail)"
    }

    /// The sentence a body that is not JSON gets, on every runtime.
    static let notJSONMessage = "decode failed: the document is not valid JSON"

    // Not private: this is the phone's WIRE PORT — where bytes off the network
    // become a Mar value — and internal/iosbundle drives it directly so the
    // port is compared with the browser's without needing a real request.
    static func makeServiceResult(data: Data?, response: URLResponse?, error: Error?, responseShape: MarWireShape?) -> MarValue {
        if error != nil {
            return .ctor(tag: "Err", args: [serviceErrorOffline()], origin: nil)
        }
        let bodyData = data ?? Data()
        let bodyString = String(data: bodyData, encoding: .utf8) ?? ""
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            let se = serviceErrorFromResponse(status: http.statusCode, body: bodyData, fallbackBody: bodyString)
            return .ctor(tag: "Err", args: [se], origin: nil)
        }
        // Two failures live here and they are not the same failure. A body
        // that is not JSON is Mar's own sentence, identical on every runtime;
        // anything after that keeps its own words. Foundation's message is
        // neither: it is LOCALIZED, so the same broken response read
        // differently depending on the language the phone was set to. Fixed
        // once already in MarWire; it was alive here too.
        let any: Any
        do {
            any = try JSONSerialization.jsonObject(with: bodyData, options: [.fragmentsAllowed])
        } catch {
            let se: MarValue = .ctor(tag: "ServerError",
                args: [.string(notJSONMessage)], origin: nil)
            return .ctor(tag: "Err", args: [se], origin: nil)
        }
        do {
            let decoded = try MarJSONCodec.jsonToMar(any)
            // The response was not produced by the type system either. It
            // comes from this app's own server, so the realistic mismatch is
            // version skew: a phone still running an embedded snapshot
            // against a server whose response type has moved. It goes where
            // a body that would not decode already goes.
            if let shape = responseShape {
                if let err = MarWire.check(decoded, shape) {
                    let se: MarValue = .ctor(tag: "ServerError",
                        args: [.string("the response does not match what this app expects: \(err)")],
                        origin: nil)
                    return .ctor(tag: "Err", args: [se], origin: nil)
                }
                return .ctor(tag: "Ok", args: [MarWire.coerce(decoded, shape)], origin: nil)
            }
            return .ctor(tag: "Ok", args: [decoded], origin: nil)
        } catch {
            let se: MarValue = .ctor(tag: "ServerError", args: [.string(notJSONMessage)], origin: nil)
            return .ctor(tag: "Err", args: [se], origin: nil)
        }
    }

    @MainActor
    private static func deliverServiceResult(_ resultValue: MarValue, toMsg: MarValue) {
        do {
            let msg = try Eval.apply(toMsg, resultValue)
            MarDispatcher.shared.dispatch(msg)
        } catch {
            print("[mar] toMsg failed: \(error.localizedDescription)")
        }
    }

    /// Service-path variant of dispatchErr: an internal failure (bad path)
    /// surfaces as Err (ServerError msg) so the value matches the
    /// Result Service.Error type user code expects.
    private static func dispatchServiceErr(_ toMsg: MarValue, message: String) {
        Task { @MainActor in
            let se: MarValue = .ctor(tag: "ServerError", args: [.string(message)], origin: nil)
            do {
                let msg = try Eval.apply(toMsg, .ctor(tag: "Err", args: [se], origin: nil))
                MarDispatcher.shared.dispatch(msg)
            } catch {
                print("[mar] error dispatch failed: \(error.localizedDescription)")
            }
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
