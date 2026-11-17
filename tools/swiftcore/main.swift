import Foundation

// A command-line driver for the iOS interpreter CORE: decode a program.json,
// evaluate a named top-level value, print it. The Swift equivalent of the
// browser's __marEvalValue, without a simulator.
let args = CommandLine.arguments

// Second mode: the failure wording a service call surfaces. It has no Go
// counterpart -- the server is what fails, the two CLIENTS are what have to
// agree about how it reads -- so this prints the phone's answer for the
// browser's test to compare against.
if args.count >= 4, args[1] == "--http-error" {
    let status = Int(args[2]) ?? 0
    let body = args[3]
    let data = body.data(using: .utf8) ?? Data()
    print(MarHTTP.decodeServerError(body: data, fallbackBody: body, status: status))
    exit(0)
}

// Third mode: the wire port itself. Bytes and a status in, a Mar value out --
// the same journey a real response makes, minus URLSession.
if args.count >= 4, args[1] == "--service-result" {
    let status = Int(args[2]) ?? 0
    let body = args[3]
    let data = body.data(using: .utf8) ?? Data()
    let response = HTTPURLResponse(url: URL(string: "https://example.invalid/api/ping")!,
                                   statusCode: status, httpVersion: nil, headerFields: nil)
    let v = MarHTTP.makeServiceResult(data: data, response: response, error: nil, responseShape: nil)
    // Print the Err's message, or "Ok <value>", so both clients are compared
    // on the sentence a program actually receives.
    if case .ctor(let tag, let cargs) = v {
        if tag == "Err", let inner = cargs.first, case .ctor(let etag, let eargs) = inner {
            if let m = eargs.first, case .string(let s) = m { print(s) } else { print(etag) }
        } else if let ok = cargs.first {
            print("Ok " + String(describing: ok))
        }
    }
    exit(0)
}

// Fourth mode: the AUTH port. Same journey as --service-result, through the
// sign-in endpoint's decoder, so the user record the app is handed is compared
// with the browser's for the same body.
if args.count >= 4, args[1] == "--auth-verify" {
    let status = Int(args[2]) ?? 0
    let body = args[3]
    let data = body.data(using: .utf8) ?? Data()
    let response = HTTPURLResponse(url: URL(string: "https://example.invalid/_auth/verify-code")!,
                                   statusCode: status, httpVersion: nil, headerFields: nil)
    // { id : Int, email : String }: the shape the probe program declares,
    // unless a fifth argument carries one, as the checker stamped it on a
    // program's Auth.verifyCode. Built through decode rather than by hand,
    // which is both the real path and the only one now that a shape's fields
    // are private(set) -- built once, then read, and the compiler keeps it
    // that way.
    // A fifth argument that is not a shape is refused, not replaced by the
    // default: a test that passed its program's shape and silently got
    // {id, email} would be checking nothing.
    var given: Any? = nil
    if args.count >= 5 {
        guard let obj = try? JSONSerialization.jsonObject(with: Data(args[4].utf8)),
              MarWireShape.decode(obj) != nil else {
            FileHandle.standardError.write(Data("error: --auth-verify: the fifth argument is not a wire shape\n".utf8))
            exit(2)
        }
        given = obj
    }
    let shape = MarWireShape.decode(given ?? [
        "kind": "Record",
        "fields": [
            ["name": "id", "shape": ["kind": "Int"]],
            ["name": "email", "shape": ["kind": "String"]],
        ],
    ])!
    let v = MarHTTP.makeAuthOutcomeResult(
        data: data, response: response, error: nil,
        okOutcome: { user in .ctor(tag: "SignedIn", args: [user]) },
        mapCode: MarHTTP.verifyCodeOutcome,
        userShape: shape)
    if case .ctor(let tag, let cargs) = v {
        if tag == "Err", let inner = cargs.first, case .ctor(let etag, let eargs) = inner {
            if let m = eargs.first, case .string(let s) = m { print(s) } else { print(etag) }
        } else if let ok = cargs.first {
            // An Ok carries either the SignedIn user or a domain outcome; the
            // outcome's TAG is what a program cases on, so print that alone
            // when it has no argument.
            if case .ctor(let otag, let oargs) = ok, oargs.isEmpty {
                print("Ok " + otag)
            } else if given != nil, case .ctor("SignedIn", let uargs) = ok, let user = uargs.first,
                      let json = try? MarJSONCodec.jsonText(user) {
                // With a program's shape, the user the app is handed, written
                // back in the wire format, so it can be compared with the body
                // it was read from field by field.
                print("SignedIn " + json)
            } else {
                print("Ok " + String(describing: ok))
            }
        }
    }
    exit(0)
}

// Fifth mode: what a 401 decides. Three facts in, three answers out.
if args.count >= 5, args[1] == "--auth-expiry" {
    let d = MarHTTP.decideAuthExpiry(signInPath: args[2],
                                     alreadyRedirecting: args[3] == "true",
                                     currentPath: args[4])
    print("\(d.handled)|\(d.navigate)|\(d.capture ?? "")")
    exit(0)
}

// Sixth mode: the REQUEST a Service.call sends, built by the phone's own
// buildServiceRequest from a value in a program. The server's guard is what
// has to accept these bytes, so the test hands them to the server.
if args.count >= 6, args[1] == "--service-request" {
    MainActor.assumeIsolated {
        do {
            let data = try Data(contentsOf: URL(fileURLWithPath: args[2]))
            let program = try MarJSONCodec.decodeProgram(data)
            let env = MarBuiltins.makeEnv()
            for module in program.modules {
                try MarLoader.load(module: module, into: env)
            }
            guard let v = env.lookup(args[5]) else {
                FileHandle.standardError.write("value not found: \(args[5])\n".data(using: .utf8)!)
                exit(1)
            }
            let built = MarBuiltins.buildServiceRequest(verb: args[3], pattern: args[4], req: v)
            print(built.url)
            print(built.body ?? "")
        } catch {
            FileHandle.standardError.write("error: \(error)\n".data(using: .utf8)!)
            exit(1)
        }
    }
    exit(0)
}

// Seventh mode: the wire round trip (parityfixture.WireCases). One process for
// the whole table, because it is a few hundred probes:
//
//   values     each named value, rendered by wireDump
//   requests   the URL and body the phone's Service.call writes for a
//              contract and a request value: buildServiceRequest, the function
//              both Service.call builtins call before handing the bytes to
//              URLSession unchanged
//   responses  a body the SERVER wrote, through makeServiceResult with the
//              shape the call site's Service.call carries, rendered by wireDump
//
// wireDump has to render exactly what wireparity.Dump renders in Go and `dump`
// renders in the browser test: it is the one thing the three sides share.
func wireDump(_ v: MarValue) -> String {
    func all(_ xs: [MarValue]) -> String { "[" + xs.map(wireDump).joined(separator: ",") + "]" }
    switch v {
    case .int(let n): return "I\(n)"
    case .decimal(let d): return "D" + DecMath.decString(d)
    case .string(let s): return "S[" + s.unicodeScalars.map { String($0.value) }.joined(separator: ",") + "]"
    case .bool(let b): return b ? "B1" : "B0"
    case .unit: return "U"
    case .char(let c): return "Ch\(c.value)"
    case .time(let ms): return "Tm\(ms)"
    case .angle(let d): return "A\(d)"
    case .duration(let ms): return "Du\(ms)"
    case .list(let xs): return "L" + all(xs)
    case .tuple(let xs): return "Tu" + all(xs)
    case .record(let fs, _):
        return "R{" + fs.keys.sorted().map { "\($0)=" + wireDump(fs[$0]!) }.joined(separator: ",") + "}"
    case .ctor(let tag, let cargs): return "K" + tag + all(cargs)
    case .dict(let pairs):
        return "Di[" + pairs.map { "(" + wireDump($0.key) + ">" + wireDump($0.value) + ")" }.joined(separator: ",") + "]"
    case .set(let items): return "Se" + all(items)
    default: return "?"
    }
}

if args.count >= 4, args[1] == "--wire-batch" {
    MainActor.assumeIsolated {
        do {
            let program = try MarJSONCodec.decodeProgram(try Data(contentsOf: URL(fileURLWithPath: args[2])))
            let env = MarBuiltins.makeEnv()
            for module in program.modules { try MarLoader.load(module: module, into: env) }
            let plan = try JSONSerialization.jsonObject(with: try Data(contentsOf: URL(fileURLWithPath: args[3]))) as! [String: Any]
            func lookup(_ n: String) throws -> MarValue {
                guard let v = env.lookup("Main." + n) else {
                    throw MarRuntimeError.message("value not found: Main.\(n)")
                }
                return v
            }
            var values: [String: String] = [:]
            for n in plan["values"] as? [String] ?? [] {
                do { values[n] = wireDump(try lookup(n)) } catch { values[n] = "THREW \(error)" }
            }
            var requests: [String: Any] = [:]
            for r in plan["requests"] as? [[String: Any]] ?? [] {
                let name = r["name"] as! String
                do {
                    guard case .ctor("__Service", let sargs) = try lookup(r["service"] as! String),
                          sargs.count == 2, case .string(let verb) = sargs[0], case .string(let pattern) = sargs[1] else {
                        throw MarRuntimeError.message("not a service contract")
                    }
                    // The value Service.call is handed: the case's value,
                    // whole or as the `v` field of the request record.
                    let v = try lookup(r["value"] as! String)
                    let req: MarValue = (r["whole"] as? Bool ?? false) ? v : .record(fields: ["v": v], order: ["v"])
                    let built = MarBuiltins.buildServiceRequest(verb: verb, pattern: pattern, req: req)
                    requests[name] = ["url": built.url, "method": verb, "body": built.body as Any? ?? NSNull()]
                } catch {
                    requests[name] = ["threw": "\(error)"]
                }
            }
            var responses: [String: String] = [:]
            for r in plan["responses"] as? [[String: Any]] ?? [] {
                let name = r["name"] as! String
                let shape = MarWireShape.decode(r["shape"])
                if shape == nil {
                    responses[name] = "NO SHAPE"
                    continue
                }
                let response = HTTPURLResponse(url: URL(string: "https://example.invalid/w")!,
                                               statusCode: 200, httpVersion: nil, headerFields: nil)
                let result = MarHTTP.makeServiceResult(data: Data((r["body"] as! String).utf8),
                                                       response: response, error: nil, responseShape: shape)
                responses[name] = wireDump(result)
            }
            let out = try JSONSerialization.data(withJSONObject: ["values": values, "requests": requests, "responses": responses],
                                                 options: [.sortedKeys])
            FileHandle.standardOutput.write(out)
            exit(0)
        } catch {
            FileHandle.standardError.write(Data("error: \(error)\n".utf8))
            exit(1)
        }
    }
}

guard args.count >= 3 else {
    FileHandle.standardError.write("usage: driver <program.json> <Module.value>\n".data(using: .utf8)!)
    exit(2)
}
// The interpreter is @MainActor now, and top-level code is not. This driver
// is single-threaded by construction, so assumeIsolated states that rather
// than hopping through an await the rest of the file would have to follow.
MainActor.assumeIsolated {
do {
    let data = try Data(contentsOf: URL(fileURLWithPath: args[1]))
    let program = try MarJSONCodec.decodeProgram(data)
    let env = MarBuiltins.makeEnv()
    for module in program.modules {
        try MarLoader.load(module: module, into: env)
    }
    guard let v = env.lookup(args[2]) else {
        FileHandle.standardError.write("value not found: \(args[2])\n".data(using: .utf8)!)
        exit(1)
    }
    switch v {
    case .string(let x): print(x)
    case .int(let x): print(x)
    case .bool(let x): print(x)
    default: print(String(describing: v))
    }
} catch {
    // The sentence a person would read, so a test can compare a RAISED error
    // with the other runtimes' (parityfixture.DecimalBoundCases does).
    let text = (error as? LocalizedError)?.errorDescription ?? "\(error)"
    FileHandle.standardError.write("error: \(text)\n".data(using: .utf8)!)
    exit(1)
}
}
