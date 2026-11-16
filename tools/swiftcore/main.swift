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
    // { id : Int, email : String }: the shape the probe program declares.
    // Built through decode rather than by hand, which is both the real path
    // and the only one now that a shape's fields are private(set) -- built
    // once, then read, and the compiler keeps it that way.
    let shape = MarWireShape.decode([
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
