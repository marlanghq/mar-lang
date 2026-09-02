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
