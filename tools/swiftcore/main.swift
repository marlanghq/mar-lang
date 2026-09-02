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
    if case .ctor(let tag, let cargs, _) = v {
        if tag == "Err", let inner = cargs.first, case .ctor(let etag, let eargs, _) = inner {
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
    let shape = MarWireShape(kind: "Record")
    let idShape = MarWireShape(kind: "Int")
    let emailShape = MarWireShape(kind: "String")
    shape.fields = [(name: "id", shape: idShape), (name: "email", shape: emailShape)]
    let v = MarHTTP.makeAuthOutcomeResult(
        data: data, response: response, error: nil,
        okOutcome: { user in .ctor(tag: "SignedIn", args: [user], origin: nil) },
        mapCode: { code in
            switch code {
            case "invalid_code": return .ctor(tag: "WrongCode", args: [], origin: nil)
            case "too_many_attempts": return .ctor(tag: "TooManyAttempts", args: [], origin: nil)
            default: return nil
            }
        },
        userShape: shape)
    if case .ctor(let tag, let cargs, _) = v {
        if tag == "Err", let inner = cargs.first, case .ctor(let etag, let eargs, _) = inner {
            if let m = eargs.first, case .string(let s) = m { print(s) } else { print(etag) }
        } else if let ok = cargs.first {
            print("Ok " + String(describing: ok))
        }
    }
    exit(0)
}

guard args.count >= 3 else {
    FileHandle.standardError.write("usage: driver <program.json> <Module.value>\n".data(using: .utf8)!)
    exit(2)
}
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
    FileHandle.standardError.write("error: \(error)\n".data(using: .utf8)!)
    exit(1)
}
