// All the natively-implemented mar functions the user code can call.
// One-to-one port of `makeBuiltinEnv` in internal/jsserve/runtime.js.
//
// Two naming conventions register every builtin: the snake/camel name
// the parser desugars qualified calls into (`stringFromInt`,
// `navigationStack`, ...) and the dotted name a user can also write
// directly (`String.fromInt`, `UI.navigationStack`). Same value, two keys.

import Foundation

enum MarBuiltins {

    // makeEnv defines every runtime builtin in one place, organized
    // by section markers. The length is intentional — this is the
    // dispatch table for the language; splitting it would scatter
    // related entries.
    // swiftlint:disable:next function_body_length
    static func makeEnv() -> Env {
        let env = Env(parent: nil)

        // MARK: Booleans / Maybe / Result
        env.define("True",  .bool(true))
        env.define("False", .bool(false))
        env.define("Nothing", .ctor(tag: "Nothing", args: [], origin: nil))
        env.define("Just",  .fn(MarFn.native(1) { .ctor(tag: "Just", args: $0, origin: nil) }))
        env.define("Ok",    .fn(MarFn.native(1) { .ctor(tag: "Ok",   args: $0, origin: nil) }))
        env.define("Err",   .fn(MarFn.native(1) { .ctor(tag: "Err",  args: $0, origin: nil) }))

        // MARK: Arithmetic (integer for now; matches runtime.js)
        env.define("+", .fn(MarFn.native(2) { args in .int(asInt(args[0]) + asInt(args[1])) }))
        env.define("-", .fn(MarFn.native(2) { args in .int(asInt(args[0]) - asInt(args[1])) }))
        env.define("*", .fn(MarFn.native(2) { args in .int(asInt(args[0]) * asInt(args[1])) }))
        env.define("/", .fn(MarFn.native(2) { args in
            let b = asInt(args[1])
            return .int(b == 0 ? 0 : asInt(args[0]) / b)
        }))

        // MARK: Comparison
        env.define("==", .fn(MarFn.native(2) { args in .bool(args[0].equalsMar(args[1])) }))
        env.define("/=", .fn(MarFn.native(2) { args in .bool(!args[0].equalsMar(args[1])) }))
        env.define("<",  .fn(MarFn.native(2) { args in .bool(args[0].compareMar(args[1]) <  0) }))
        env.define(">",  .fn(MarFn.native(2) { args in .bool(args[0].compareMar(args[1]) >  0) }))
        env.define("<=", .fn(MarFn.native(2) { args in .bool(args[0].compareMar(args[1]) <= 0) }))
        env.define(">=", .fn(MarFn.native(2) { args in .bool(args[0].compareMar(args[1]) >= 0) }))

        // MARK: Logic
        env.define("&&", .fn(MarFn.native(2) { args in .bool(asBool(args[0]) && asBool(args[1])) }))
        env.define("||", .fn(MarFn.native(2) { args in .bool(asBool(args[0]) || asBool(args[1])) }))

        // MARK: Append (string + list, polymorphic)
        env.define("++", .fn(MarFn.native(2) { args in
            switch (args[0], args[1]) {
            case (.string(let a), .string(let b)): return .string(a + b)
            case (.list(let a), .list(let b)):     return .list(a + b)
            default: throw MarRuntimeError.message("++: unsupported operand types")
            }
        }))

        // MARK: Cons
        env.define("::", .fn(MarFn.native(2) { args in
            guard case .list(let tail) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            return .list([args[0]] + tail)
        }))

        // MARK: Pipes
        env.define("|>", .fn(MarFn.native(2) { args in try Eval.apply(args[1], args[0]) }))
        env.define("<|", .fn(MarFn.native(2) { args in try Eval.apply(args[0], args[1]) }))

        // MARK: String stdlib
        let stringFromInt = MarFn.native(1) { args in .string(String(asInt(args[0]))) }
        env.define("stringFromInt",   .fn(stringFromInt))
        env.define("String.fromInt",  .fn(stringFromInt))

        let stringLength = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            return .int(s.count)
        }
        env.define("stringLength",  .fn(stringLength))
        env.define("String.length", .fn(stringLength))

        // String.contains : String -> String -> Bool
        // First arg is the needle, second is the haystack —
        // pipe-friendly: `haystack |> String.contains "foo"`.
        let stringContains = MarFn.native(2) { args in
            guard case .string(let needle) = args[0],
                  case .string(let hay)    = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, String", got: Eval.typeOf(args[0]))
            }
            return .bool(hay.contains(needle))
        }
        env.define("stringContains",  .fn(stringContains))
        env.define("String.contains", .fn(stringContains))

        // String.startsWith : String -> String -> Bool
        // Same arg order: `s |> String.startsWith "prefix"`.
        let stringStartsWith = MarFn.native(2) { args in
            guard case .string(let prefix) = args[0],
                  case .string(let s)      = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, String", got: Eval.typeOf(args[0]))
            }
            return .bool(s.hasPrefix(prefix))
        }
        env.define("stringStartsWith",  .fn(stringStartsWith))
        env.define("String.startsWith", .fn(stringStartsWith))

        let stringToUpper = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            return .string(s.uppercased())
        }
        env.define("stringToUpper",  .fn(stringToUpper))
        env.define("String.toUpper", .fn(stringToUpper))

        let stringToLower = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            return .string(s.lowercased())
        }
        env.define("stringToLower",  .fn(stringToLower))
        env.define("String.toLower", .fn(stringToLower))

        // String.split : String -> String -> List String
        // First arg is the separator. Empty separator = list of single-
        // codepoint strings (matches the Go runtime's strings.Split).
        let stringSplit = MarFn.native(2) { args in
            guard case .string(let sep) = args[0],
                  case .string(let s)   = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, String", got: Eval.typeOf(args[0]))
            }
            let parts: [String]
            if sep.isEmpty {
                // Mirror strings.Split(s, "") — emit one element per
                // Unicode scalar so behaviour matches across runtimes.
                parts = s.map { String($0) }
            } else {
                parts = s.components(separatedBy: sep)
            }
            return .list(parts.map { .string($0) })
        }
        env.define("stringSplit",  .fn(stringSplit))
        env.define("String.split", .fn(stringSplit))

        // String.join : String -> List String -> String
        let stringJoin = MarFn.native(2) { args in
            guard case .string(let sep) = args[0],
                  case .list(let xs)    = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, List String", got: Eval.typeOf(args[0]))
            }
            var parts: [String] = []
            parts.reserveCapacity(xs.count)
            for x in xs {
                guard case .string(let s) = x else {
                    throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(x))
                }
                parts.append(s)
            }
            return .string(parts.joined(separator: sep))
        }
        env.define("stringJoin",  .fn(stringJoin))
        env.define("String.join", .fn(stringJoin))

        // String.trim : String -> String
        // Strip leading + trailing whitespace, including newlines, to
        // match the Go runtime's strings.TrimSpace semantics.
        let stringTrim = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            return .string(s.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        env.define("stringTrim",  .fn(stringTrim))
        env.define("String.trim", .fn(stringTrim))

        // MARK: List stdlib
        let listLength = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return .int(xs.count)
        }
        env.define("listLength",  .fn(listLength))
        env.define("List.length", .fn(listLength))

        let listMap = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            var out: [MarValue] = []
            out.reserveCapacity(xs.count)
            for x in xs { out.append(try Eval.apply(args[0], x)) }
            return .list(out)
        }
        env.define("listMap",  .fn(listMap))
        env.define("List.map", .fn(listMap))

        let listSum = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            var sum = 0
            for x in xs { sum += asInt(x) }
            return .int(sum)
        }
        env.define("listSum",  .fn(listSum))
        env.define("List.sum", .fn(listSum))

        let listFilter = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            var out: [MarValue] = []
            for x in xs {
                if case .bool(true) = try Eval.apply(args[0], x) { out.append(x) }
            }
            return .list(out)
        }
        env.define("listFilter",  .fn(listFilter))
        env.define("List.filter", .fn(listFilter))

        // MARK: List.reverse
        // Returns a new reversed list — does not mutate the source.
        let listReverse = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return .list(Array(xs.reversed()))
        }
        env.define("listReverse",  .fn(listReverse))
        env.define("List.reverse", .fn(listReverse))

        // List.foldl : (b -> a -> b) -> b -> List a -> b
        // Tight-loop fold; errors from the function abort the fold.
        let listFoldl = MarFn.native(3) { args in
            let fn = args[0]
            var acc = args[1]
            guard case .list(let xs) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[2]))
            }
            for x in xs {
                let partial = try Eval.apply(fn, acc)
                acc = try Eval.apply(partial, x)
            }
            return acc
        }
        env.define("listFoldl",  .fn(listFoldl))
        env.define("List.foldl", .fn(listFoldl))

        // List.range : Int -> Int -> List Int — inclusive of both
        // endpoints; empty list when from > to (matches the Go runtime).
        let listRange = MarFn.native(2) { args in
            guard case .int(let from) = args[0],
                  case .int(let to)   = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Int, Int", got: Eval.typeOf(args[0]))
            }
            if from > to {
                return .list([])
            }
            var out: [MarValue] = []
            out.reserveCapacity(to - from + 1)
            for i in from...to {
                out.append(.int(i))
            }
            return .list(out)
        }
        env.define("listRange",  .fn(listRange))
        env.define("List.range", .fn(listRange))

        // List.head : List a -> Maybe a
        let listHead = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            if let first = xs.first {
                return .ctor(tag: "Just", args: [first], origin: nil)
            }
            return .ctor(tag: "Nothing", args: [], origin: nil)
        }
        env.define("listHead",  .fn(listHead))
        env.define("List.head", .fn(listHead))

        // List.tail : List a -> Maybe (List a)
        // Nothing when the list is empty; Just (rest) otherwise.
        let listTail = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            if xs.isEmpty {
                return .ctor(tag: "Nothing", args: [], origin: nil)
            }
            return .ctor(tag: "Just", args: [.list(Array(xs.dropFirst()))], origin: nil)
        }
        env.define("listTail",  .fn(listTail))
        env.define("List.tail", .fn(listTail))

        let listIsEmpty = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return .bool(xs.isEmpty)
        }
        env.define("listIsEmpty",  .fn(listIsEmpty))
        env.define("List.isEmpty", .fn(listIsEmpty))

        // List.concat : List (List a) -> List a
        let listConcat = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            var out: [MarValue] = []
            for x in xs {
                guard case .list(let inner) = x else {
                    throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(x))
                }
                out.append(contentsOf: inner)
            }
            return .list(out)
        }
        env.define("listConcat",  .fn(listConcat))
        env.define("List.concat", .fn(listConcat))

        // MARK: Maybe.withDefault
        let maybeWithDefault = MarFn.native(2) { args in
            if case .ctor(let tag, let cargs, _) = args[1], tag == "Just" {
                return cargs[0]
            }
            return args[0]
        }
        env.define("maybeWithDefault",  .fn(maybeWithDefault))
        env.define("Maybe.withDefault", .fn(maybeWithDefault))

        // Maybe.map : (a -> b) -> Maybe a -> Maybe b
        let maybeMap = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Maybe", got: Eval.typeOf(args[1]))
            }
            if tag == "Just", let inner = cargs.first {
                let v = try Eval.apply(fn, inner)
                return .ctor(tag: "Just", args: [v], origin: nil)
            }
            return args[1]
        }
        env.define("maybeMap",  .fn(maybeMap))
        env.define("Maybe.map", .fn(maybeMap))

        // Maybe.andThen : (a -> Maybe b) -> Maybe a -> Maybe b
        let maybeAndThen = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Maybe", got: Eval.typeOf(args[1]))
            }
            if tag == "Just", let inner = cargs.first {
                return try Eval.apply(fn, inner)
            }
            return args[1]
        }
        env.define("maybeAndThen",  .fn(maybeAndThen))
        env.define("Maybe.andThen", .fn(maybeAndThen))

        // Result.map : (a -> b) -> Result e a -> Result e b
        let resultMap = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Result", got: Eval.typeOf(args[1]))
            }
            if tag == "Ok", let inner = cargs.first {
                let v = try Eval.apply(fn, inner)
                return .ctor(tag: "Ok", args: [v], origin: nil)
            }
            return args[1]
        }
        env.define("resultMap",  .fn(resultMap))
        env.define("Result.map", .fn(resultMap))

        // Result.andThen : (a -> Result e b) -> Result e a -> Result e b
        let resultAndThen = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Result", got: Eval.typeOf(args[1]))
            }
            if tag == "Ok", let inner = cargs.first {
                return try Eval.apply(fn, inner)
            }
            return args[1]
        }
        env.define("resultAndThen",  .fn(resultAndThen))
        env.define("Result.andThen", .fn(resultAndThen))

        // Result.mapError : (e1 -> e2) -> Result e1 a -> Result e2 a
        let resultMapError = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Result", got: Eval.typeOf(args[1]))
            }
            if tag == "Err", let inner = cargs.first {
                let v = try Eval.apply(fn, inner)
                return .ctor(tag: "Err", args: [v], origin: nil)
            }
            return args[1]
        }
        env.define("resultMapError",  .fn(resultMapError))
        env.define("Result.mapError", .fn(resultMapError))

        // MARK: View constructors

        registerViewBuiltins(env)

        // MARK: Page.create
        let pageCreate = MarFn.native(1) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: Eval.typeOf(args[0]))
            }
            // [path, init, update, view, title]
            let pathV = fs["path"] ?? .string("")
            let initV = fs["init"] ?? .unit
            let updateV = fs["update"] ?? .unit
            let viewV = fs["view"] ?? .unit
            let titleV = fs["title"] ?? .string("")
            return .ctor(tag: "__Page",
                         args: [pathV, initV, updateV, viewV, titleV],
                         origin: nil)
        }
        env.define("pageCreate",  .fn(pageCreate))
        env.define("Page.create", .fn(pageCreate))

        // MARK: Page.protected
        //
        // Same shape as Page.create plus User-aware handler
        // signatures. We emit a `__ProtectedPage` ctor (distinct
        // from `__Page`) so AppContext can detect protected pages,
        // gate them on Auth.me, and thread the User into
        // init/update/view as the first argument. The redirect
        // destination is centralized in Auth.config.signInPage —
        // the renderer reads it from AppContext at render time.
        let pageProtected = MarFn.native(1) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: Eval.typeOf(args[0]))
            }
            let pathV = fs["path"] ?? .string("")
            let initV = fs["init"] ?? .unit
            let updateV = fs["update"] ?? .unit
            let viewV = fs["view"] ?? .unit
            let titleV = fs["title"] ?? .string("")
            return .ctor(tag: "__ProtectedPage",
                         args: [pathV, initV, updateV, viewV, titleV],
                         origin: nil)
        }
        env.define("pageProtected",  .fn(pageProtected))
        env.define("Page.protected", .fn(pageProtected))

        // MARK: Page.dynamic
        //
        // Pattern path with `:param` segments. Emits `__DynamicPage`
        // so AppContext can match URLs against the pattern at
        // navigation time and thread a Params record through
        // init/update/view as the leading argument. Same record shape
        // as Page.create — only the ctor tag differs.
        let pageDynamic = MarFn.native(1) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: Eval.typeOf(args[0]))
            }
            let pathV = fs["path"] ?? .string("")
            let initV = fs["init"] ?? .unit
            let updateV = fs["update"] ?? .unit
            let viewV = fs["view"] ?? .unit
            let titleV = fs["title"] ?? .string("")
            return .ctor(tag: "__DynamicPage",
                         args: [pathV, initV, updateV, viewV, titleV],
                         origin: nil)
        }
        env.define("pageDynamic",  .fn(pageDynamic))
        env.define("Page.dynamic", .fn(pageDynamic))

        // MARK: Page.dynamicProtected
        //
        // Pattern path + auth gate. Combines __DynamicPage's URL
        // matching with __ProtectedPage's Auth.me bootstrap. Handler
        // signature is `User -> Params -> ...` (User first).
        let pageDynamicProtected = MarFn.native(1) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: Eval.typeOf(args[0]))
            }
            let pathV = fs["path"] ?? .string("")
            let initV = fs["init"] ?? .unit
            let updateV = fs["update"] ?? .unit
            let viewV = fs["view"] ?? .unit
            let titleV = fs["title"] ?? .string("")
            return .ctor(tag: "__DynamicProtectedPage",
                         args: [pathV, initV, updateV, viewV, titleV],
                         origin: nil)
        }
        env.define("pageDynamicProtected",  .fn(pageDynamicProtected))
        env.define("Page.dynamicProtected", .fn(pageDynamicProtected))

        // MARK: Nav.push / Nav.replace
        //
        // Programmatic navigation. Reads the URL string from the
        // effect args and asks AppContext to swap the active page.
        // Replace also invalidates the cached User so a logout flow
        // re-fetches /_auth/me on the next protected entry.
        let navPush = MarFn.native(1) { args in
            let path: String
            if case .string(let s) = args[0] { path = s } else { path = "" }
            return .effect(MarEffect(tag: "navPush") {
                Task { @MainActor in AppContext.shared.navigate(path: path, replace: false) }
                return .unit
            })
        }
        env.define("navPush",  .fn(navPush))
        env.define("Nav.push", .fn(navPush))

        let navReplace = MarFn.native(1) { args in
            let path: String
            if case .string(let s) = args[0] { path = s } else { path = "" }
            return .effect(MarEffect(tag: "navReplace") {
                Task { @MainActor in AppContext.shared.navigate(path: path, replace: true) }
                return .unit
            })
        }
        env.define("navReplace",  .fn(navReplace))
        env.define("Nav.replace", .fn(navReplace))

        // Nav.afterSignIn : Effect e msg
        // Drains the framework-managed `pendingReturnPath` set when a
        // 401 redirected the user to sign-in; navigates there. Falls
        // back to "/" when no return target was captured (user landed
        // on sign-in directly, or this is the first cold start).
        // Resets the redirect-coalescer so the next genuine session
        // expiry triggers a fresh redirect.
        let navAfterSignIn = MarEffect(tag: "navAfterSignIn") {
            Task { @MainActor in
                let target = AppContext.shared.pendingReturnPath ?? "/"
                AppContext.shared.pendingReturnPath = nil
                AppContext.shared.redirectingToSignIn = false
                AppContext.shared.navigate(path: target, replace: true)
            }
            return .unit
        }
        env.define("navAfterSignIn",  .effect(navAfterSignIn))
        env.define("Nav.afterSignIn", .effect(navAfterSignIn))

        // MARK: linkTo / Nav.pushTo / Nav.replaceTo
        //
        // Type-safe navigation built on top of `Path r`. The user
        // passes a Path (a String at runtime — the typechecker
        // enforces the surface contract) plus the params record;
        // MarPath parses the pattern, validates the record's shape,
        // and renders the URL.
        //
        // linkTo is pure (returns the URL string); Nav.pushTo and
        // Nav.replaceTo wrap the result in an Effect that drives
        // AppContext.navigate(path:replace:) — same hook used by
        // the older Nav.push / Nav.replace.
        let linkTo = MarFn.native(2) { args in
            guard case .string(let src) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Path", got: Eval.typeOf(args[0]))
            }
            let pattern = MarPath.parse(src)
            let url = try MarPath.build(pattern, params: args[1])
            return .string(url)
        }
        env.define("linkTo", .fn(linkTo))

        let navPushTo = MarFn.native(2) { args in
            guard case .string(let src) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Path", got: Eval.typeOf(args[0]))
            }
            let pattern = MarPath.parse(src)
            // Build eagerly so missing-param / wrong-type errors fire
            // at the call site, not later inside the Effect closure.
            let url = try MarPath.build(pattern, params: args[1])
            return .effect(MarEffect(tag: "navPushTo") {
                Task { @MainActor in AppContext.shared.navigate(path: url, replace: false) }
                return .unit
            })
        }
        env.define("navPushTo",  .fn(navPushTo))
        env.define("Nav.pushTo", .fn(navPushTo))

        let navReplaceTo = MarFn.native(2) { args in
            guard case .string(let src) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Path", got: Eval.typeOf(args[0]))
            }
            let pattern = MarPath.parse(src)
            let url = try MarPath.build(pattern, params: args[1])
            return .effect(MarEffect(tag: "navReplaceTo") {
                Task { @MainActor in AppContext.shared.navigate(path: url, replace: true) }
                return .unit
            })
        }
        env.define("navReplaceTo",  .fn(navReplaceTo))
        env.define("Nav.replaceTo", .fn(navReplaceTo))

        // MARK: App.frontend / backend / fullstack
        //
        // Each returns an Effect that, when run, captures the page
        // list (or no-op for backend) into the global AppContext so
        // the iOS shell can read it back. Mirrors the JS overrides
        // for `mar dev` / `mar build`.
        // App.frontend / fullstack effects run synchronously from
        // AppViewModel.runProgram, which is @MainActor. We assert
        // that here so the effect closure can call into the
        // MainActor-isolated AppContext.
        let appFrontend = MarFn.native(1) { args in
            return .effect(MarEffect(tag: "mountPages") {
                MainActor.assumeIsolated {
                    AppContext.shared.capturePages(args[0])
                }
                return .unit
            })
        }
        env.define("appFrontend",   .fn(appFrontend))
        env.define("App.frontend",  .fn(appFrontend))

        let appBackend = MarFn.native(1) { _ in
            .effect(MarEffect(tag: "noop") { .unit })
        }
        env.define("appBackend",  .fn(appBackend))
        env.define("App.backend", .fn(appBackend))

        let appFullstack = MarFn.native(1) { args in
            return .effect(MarEffect(tag: "mountPages") {
                if case .record(let fs, _) = args[0], let pages = fs["pages"] {
                    MainActor.assumeIsolated {
                        AppContext.shared.capturePages(pages)
                    }
                }
                return .unit
            })
        }
        env.define("appFullstack",  .fn(appFullstack))
        env.define("App.fullstack", .fn(appFullstack))

        // MARK: Effect.* — sync helpers
        let effectSucceed = MarFn.native(1) { args in
            .effect(MarEffect(tag: "pure") { args[0] })
        }
        env.define("effectSucceed",  .fn(effectSucceed))
        env.define("Effect.succeed", .fn(effectSucceed))

        // Effect.fail : e -> Effect e a — throws when run, carrying
        // the user-supplied error value. Mirror of the Go runtime's
        // effectError; if the failure isn't caught upstream, it
        // surfaces as a dispatcher-level error.
        let effectFail = MarFn.native(1) { args in
            let err = args[0]
            return .effect(MarEffect(tag: "fail") {
                // Most user code passes a String; if it's anything else,
                // fall through to the type name so we still surface a
                // useful message rather than swallowing it.
                let msg: String
                if case .string(let s) = err {
                    msg = s
                } else {
                    msg = "(non-string error: " + Eval.typeOf(err) + ")"
                }
                throw MarRuntimeError.message("Effect.fail: " + msg)
            })
        }
        env.define("effectFail",  .fn(effectFail))
        env.define("Effect.fail", .fn(effectFail))

        // Effect.forEach : (a -> Effect e ()) -> List a -> Effect e ()
        // Sequential — each effect runs in order, halting on the first
        // error. Returns unit on success.
        let effectForEach = MarFn.native(2) { args in
            let fn = args[0]
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            return .effect(MarEffect(tag: "forEach") {
                for x in xs {
                    let v = try Eval.apply(fn, x)
                    guard case .effect(let eff) = v else {
                        throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(v))
                    }
                    _ = try eff.run()
                }
                return .unit
            })
        }
        env.define("effectForEach",  .fn(effectForEach))
        env.define("Effect.forEach", .fn(effectForEach))

        // Effect.sequence : List (Effect e a) -> Effect e (List a)
        // Runs each effect, collecting the results into a list.
        let effectSequence = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return .effect(MarEffect(tag: "sequence") {
                var out: [MarValue] = []
                out.reserveCapacity(xs.count)
                for x in xs {
                    guard case .effect(let eff) = x else {
                        throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(x))
                    }
                    out.append(try eff.run())
                }
                return .list(out)
            })
        }
        env.define("effectSequence",  .fn(effectSequence))
        env.define("Effect.sequence", .fn(effectSequence))

        let effectMap = MarFn.native(2) { args in
            guard case .effect(let inner) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(args[1]))
            }
            let fn = args[0]
            return .effect(MarEffect(tag: "map") {
                let v = try inner.run()
                return try Eval.apply(fn, v)
            })
        }
        env.define("effectMap",  .fn(effectMap))
        env.define("Effect.map", .fn(effectMap))

        let effectAndThen = MarFn.native(2) { args in
            guard case .effect(let inner) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(args[1]))
            }
            let fn = args[0]
            return .effect(MarEffect(tag: "andThen") {
                let v = try inner.run()
                let next = try Eval.apply(fn, v)
                guard case .effect(let outer) = next else {
                    throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(next))
                }
                return try outer.run()
            })
        }
        env.define("effectAndThen",  .fn(effectAndThen))
        env.define("Effect.andThen", .fn(effectAndThen))

        env.define("effectNone",  .effect(MarEffect(tag: "none") { .unit }))
        env.define("Effect.none", .effect(MarEffect(tag: "none") { .unit }))

        // MARK: Time — Duration type + unit smart constructors
        let mkDuration: (Int) -> MarFn = { mult in
            MarFn.native(1) { args in
                guard case .int(let n) = args[0] else {
                    throw MarRuntimeError.typeMismatch(expected: "Int", got: Eval.typeOf(args[0]))
                }
                return .duration(n * mult)
            }
        }
        env.define("timeSeconds", .fn(mkDuration(1)));         env.define("Time.seconds", .fn(mkDuration(1)))
        env.define("timeMinutes", .fn(mkDuration(60)));        env.define("Time.minutes", .fn(mkDuration(60)))
        env.define("timeHours",   .fn(mkDuration(60*60)));     env.define("Time.hours",   .fn(mkDuration(60*60)))
        env.define("timeDays",    .fn(mkDuration(24*60*60)));  env.define("Time.days",    .fn(mkDuration(24*60*60)))
        env.define("timeWeeks",   .fn(mkDuration(7*24*60*60)));env.define("Time.weeks",   .fn(mkDuration(7*24*60*60)))
        let timeToSeconds = MarFn.native(1) { args in
            guard case .duration(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Duration", got: Eval.typeOf(args[0]))
            }
            return .int(s)
        }
        env.define("timeToSeconds",  .fn(timeToSeconds))
        env.define("Time.toSeconds", .fn(timeToSeconds))

        // Absolute moments. Time.now reads the wall clock as an
        // Effect; arithmetic shifts moments by Durations; .diff
        // produces a Duration between two moments.
        let timeNow = MarEffect(tag: "timeNow") {
            .time(Int(Date().timeIntervalSince1970 * 1000))
        }
        env.define("timeNow",  .effect(timeNow))
        env.define("Time.now", .effect(timeNow))

        let timeAdd = MarFn.native(2) { args in
            guard case .time(let t) = args[0],
                  case .duration(let d) = args[1] else {
                throw MarRuntimeError.typeMismatch(
                    expected: "(Time, Duration)",
                    got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
            }
            return .time(t + d * 1000)
        }
        env.define("timeAdd",  .fn(timeAdd))
        env.define("Time.add", .fn(timeAdd))

        let timeSub = MarFn.native(2) { args in
            guard case .time(let t) = args[0],
                  case .duration(let d) = args[1] else {
                throw MarRuntimeError.typeMismatch(
                    expected: "(Time, Duration)",
                    got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
            }
            return .time(t - d * 1000)
        }
        env.define("timeSub",  .fn(timeSub))
        env.define("Time.sub", .fn(timeSub))

        let timeDiff = MarFn.native(2) { args in
            guard case .time(let a) = args[0],
                  case .time(let b) = args[1] else {
                throw MarRuntimeError.typeMismatch(
                    expected: "(Time, Time)",
                    got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
            }
            return .duration((b - a) / 1000)
        }
        env.define("timeDiff",  .fn(timeDiff))
        env.define("Time.diff", .fn(timeDiff))

        let mkCompare: (@escaping (Int, Int) -> Bool) -> MarFn = { op in
            MarFn.native(2) { args in
                guard case .time(let a) = args[0], case .time(let b) = args[1] else {
                    throw MarRuntimeError.typeMismatch(
                        expected: "(Time, Time)",
                        got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
                }
                return .bool(op(a, b))
            }
        }
        env.define("timeBefore",  .fn(mkCompare(<)))
        env.define("Time.before", .fn(mkCompare(<)))
        env.define("timeAfter",   .fn(mkCompare(>)))
        env.define("Time.after",  .fn(mkCompare(>)))

        let isoFormatter: ISO8601DateFormatter = {
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime]
            return f
        }()
        let timeToIso = MarFn.native(1) { args in
            guard case .time(let ms) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Time", got: Eval.typeOf(args[0]))
            }
            let d = Date(timeIntervalSince1970: TimeInterval(ms) / 1000)
            return .string(isoFormatter.string(from: d))
        }
        env.define("timeToIso",  .fn(timeToIso))
        env.define("Time.toIso", .fn(timeToIso))

        let timeFromIso = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            if let d = isoFormatter.date(from: s) {
                return .ctor(tag: "Just",
                             args: [.time(Int(d.timeIntervalSince1970 * 1000))],
                             origin: nil)
            }
            return .ctor(tag: "Nothing", args: [], origin: nil)
        }
        env.define("timeFromIso",  .fn(timeFromIso))
        env.define("Time.fromIso", .fn(timeFromIso))

        let timeToMillis = MarFn.native(1) { args in
            guard case .time(let ms) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Time", got: Eval.typeOf(args[0]))
            }
            return .int(ms)
        }
        env.define("timeToMillis",  .fn(timeToMillis))
        env.define("Time.toMillis", .fn(timeToMillis))

        // Calendar-aware constructors and arithmetic. Uses
        // Calendar(.gregorian) pinned to UTC so behavior matches
        // the Go and JS runtimes — months/years normalize the same
        // way (Jan 31 + 1 month = Mar 3) and there's no DST
        // weirdness since everything's UTC.
        var utcCalendar = Calendar(identifier: .gregorian)
        utcCalendar.timeZone = TimeZone(identifier: "UTC") ?? .current

        let timeFromYMD = MarFn.native(3) { args in
            guard case .int(let y) = args[0],
                  case .int(let m) = args[1],
                  case .int(let d) = args[2] else {
                throw MarRuntimeError.typeMismatch(
                    expected: "(Int, Int, Int)",
                    got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])), \(Eval.typeOf(args[2])))")
            }
            var components = DateComponents()
            components.year = y; components.month = m; components.day = d
            components.hour = 0; components.minute = 0; components.second = 0
            components.timeZone = TimeZone(identifier: "UTC")
            let date = utcCalendar.date(from: components) ?? Date(timeIntervalSince1970: 0)
            return .time(Int(date.timeIntervalSince1970 * 1000))
        }
        env.define("timeFromYMD",  .fn(timeFromYMD))
        env.define("Time.fromYMD", .fn(timeFromYMD))

        let mkCalendarShift: (Calendar.Component) -> MarFn = { component in
            MarFn.native(2) { args in
                guard case .time(let ms) = args[0], case .int(let n) = args[1] else {
                    throw MarRuntimeError.typeMismatch(
                        expected: "(Time, Int)",
                        got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
                }
                let base = Date(timeIntervalSince1970: TimeInterval(ms) / 1000)
                let shifted = utcCalendar.date(byAdding: component, value: n, to: base) ?? base
                return .time(Int(shifted.timeIntervalSince1970 * 1000))
            }
        }
        env.define("timeAddDays",    .fn(mkCalendarShift(.day)))
        env.define("Time.addDays",   .fn(mkCalendarShift(.day)))
        env.define("timeAddMonths",  .fn(mkCalendarShift(.month)))
        env.define("Time.addMonths", .fn(mkCalendarShift(.month)))
        env.define("timeAddYears",   .fn(mkCalendarShift(.year)))
        env.define("Time.addYears",  .fn(mkCalendarShift(.year)))

        // Component getters (UTC). Calendar.component returns the
        // calendar field for a Date — month is 1-indexed natively,
        // matching what the Go and JS runtimes expose.
        let mkComponent: (Calendar.Component) -> MarFn = { component in
            MarFn.native(1) { args in
                guard case .time(let ms) = args[0] else {
                    throw MarRuntimeError.typeMismatch(expected: "Time", got: Eval.typeOf(args[0]))
                }
                let date = Date(timeIntervalSince1970: TimeInterval(ms) / 1000)
                return .int(utcCalendar.component(component, from: date))
            }
        }
        env.define("timeYear",    .fn(mkComponent(.year)));    env.define("Time.year",   .fn(mkComponent(.year)))
        env.define("timeMonth",   .fn(mkComponent(.month)));   env.define("Time.month",  .fn(mkComponent(.month)))
        env.define("timeDay",     .fn(mkComponent(.day)));     env.define("Time.day",    .fn(mkComponent(.day)))
        env.define("timeHour",    .fn(mkComponent(.hour)));    env.define("Time.hour",   .fn(mkComponent(.hour)))
        env.define("timeMinute",  .fn(mkComponent(.minute)));  env.define("Time.minute", .fn(mkComponent(.minute)))
        env.define("timeSecond",  .fn(mkComponent(.second)));  env.define("Time.second", .fn(mkComponent(.second)))

        // MARK: JSON encode / decode
        env.define("jsonDecode", .fn(MarFn.native(1) { args in
            guard case .string(let raw) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            do {
                let any = try JSONSerialization.jsonObject(
                    with: Data(raw.utf8),
                    options: [.fragmentsAllowed]
                )
                return .ctor(tag: "Ok", args: [MarJSONCodec.jsonToMar(any)], origin: nil)
            } catch {
                return .ctor(tag: "Err",
                             args: [.string(error.localizedDescription)],
                             origin: nil)
            }
        }))
        env.define("JSON.decode", env.lookup("jsonDecode")!)

        env.define("jsonEncode", .fn(MarFn.native(1) { args in
            let any = MarJSONCodec.marToJSON(args[0])
            do {
                let data = try JSONSerialization.data(
                    withJSONObject: any,
                    options: [.fragmentsAllowed]
                )
                return .string(String(data: data, encoding: .utf8) ?? "")
            } catch {
                throw MarRuntimeError.message("JSON.encode failed: \(error.localizedDescription)")
            }
        }))
        env.define("JSON.encode", env.lookup("jsonEncode")!)

        // MARK: Entity / Repo / Endpoint stubs
        //
        // These exist server-side; the client never invokes them, but
        // shared modules may still mention them at top level. We give
        // each a value that survives evaluation without errors.
        let entityStub = MarFn.native(2) { _ in .ctor(tag: "__Entity", args: [], origin: nil) }
        env.define("entityDefine",  .fn(entityStub))
        env.define("Entity.define", .fn(entityStub))

        let colSerial = MarValue.ctor(tag: "__Column", args: [.string("serial")], origin: nil)
        env.define("entitySerial",  colSerial)
        env.define("Entity.serial", colSerial)

        let colCtor = MarFn.native(1) { _ in .ctor(tag: "__Column", args: [.string("?")], origin: nil) }
        env.define("entityInt",       .fn(colCtor)); env.define("Entity.int",       .fn(colCtor))
        env.define("entityText",      .fn(colCtor)); env.define("Entity.text",      .fn(colCtor))
        env.define("entityBool",      .fn(colCtor)); env.define("Entity.bool",      .fn(colCtor))
        env.define("entityTimestamp", .fn(colCtor)); env.define("Entity.timestamp", .fn(colCtor))

        let constraintStub = MarValue.ctor(tag: "__Constraint", args: [], origin: nil)
        env.define("entityNotNull",  constraintStub)
        env.define("Entity.notNull", constraintStub)

        let repoServerOnly: (String) -> MarFn = { name in
            MarFn.native(1) { _ in
                .effect(MarEffect(tag: name) {
                    throw MarRuntimeError.message("\(name) runs only server-side")
                })
            }
        }
        env.define("repoAll",          .fn(repoServerOnly("Repo.all")))
        env.define("Repo.all",         .fn(repoServerOnly("Repo.all")))
        env.define("repoFindById",     .fn(MarFn.native(2) { _ in
            .effect(MarEffect(tag: "Repo.findById") { throw MarRuntimeError.message("Repo.findById runs only server-side") })
        }))
        env.define("Repo.findById",    env.lookup("repoFindById")!)
        env.define("repoCreate",       .fn(MarFn.native(2) { _ in
            .effect(MarEffect(tag: "Repo.create") { throw MarRuntimeError.message("Repo.create runs only server-side") })
        }))
        env.define("Repo.create",      env.lookup("repoCreate")!)

        // Endpoint.* — typed handle for routes; Endpoint.call uses fetch.
        let endpointGet    = MarFn.native(1) { args in .ctor(tag: "__Ep", args: [.string("GET"),    args[0]], origin: nil) }
        let endpointPost   = MarFn.native(1) { args in .ctor(tag: "__Ep", args: [.string("POST"),   args[0]], origin: nil) }
        let endpointPatch  = MarFn.native(1) { args in .ctor(tag: "__Ep", args: [.string("PATCH"),  args[0]], origin: nil) }
        let endpointDelete = MarFn.native(1) { args in .ctor(tag: "__Ep", args: [.string("DELETE"), args[0]], origin: nil) }
        env.define("endpointGet",     .fn(endpointGet));    env.define("Endpoint.get",    .fn(endpointGet))
        env.define("endpointPost",    .fn(endpointPost));   env.define("Endpoint.post",   .fn(endpointPost))
        env.define("endpointPatch",   .fn(endpointPatch));  env.define("Endpoint.patch",  .fn(endpointPatch))
        env.define("endpointDelete",  .fn(endpointDelete)); env.define("Endpoint.delete", .fn(endpointDelete))

        // Endpoint.call : String -> Endpoint -> String -> (Result -> msg) -> Effect
        let endpointCall = MarFn.native(4) { args in
            let base = asString(args[0])
            guard case .ctor(_, let epArgs, _) = args[1],
                  epArgs.count == 2,
                  case .string(let method) = epArgs[0],
                  case .string(let path) = epArgs[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Endpoint", got: Eval.typeOf(args[1]))
            }
            let body = asString(args[2])
            let toMsg = args[3]
            return .effect(MarEffect(tag: "endpointCall") {
                MarHTTP.fire(
                    method: method,
                    url: base + path,
                    body: (method == "GET" || method == "DELETE") ? nil : body,
                    toMsg: toMsg
                )
                return .unit
            })
        }
        env.define("endpointCall",  .fn(endpointCall))
        env.define("Endpoint.call", .fn(endpointCall))

        // MARK: Service — RPC over HTTP

        // Service.declare : Service req resp — typed contract with no
        // handler. Each top-level binding gets its own stamped copy
        // via the loader's value-semantics path. The opaque __Service
        // ctor carries provenance so Service.call can derive the URL.
        let serviceDeclareValue: MarValue = .ctor(tag: "__Service", args: [], origin: nil)
        env.define("serviceDeclare",  serviceDeclareValue)
        env.define("Service.declare", serviceDeclareValue)

        // Service.implement : Service req resp -> (req -> Effect String resp) -> ExposedService
        // Browser/iOS-side, the handler never runs — service handlers
        // live on the server. We just return the contract back so the
        // value evaluates and Service.call still finds the origin.
        let serviceImplement = MarFn.native(2) { args in args[0] }
        env.define("serviceImplement",  .fn(serviceImplement))
        env.define("Service.implement", .fn(serviceImplement))

        // Service.call : Service req resp -> req -> (Result -> msg) -> Effect
        let serviceCall = MarFn.native(3) { args in
            let svc = args[0]
            let req = args[1]
            let toMsg = args[2]
            return .effect(MarEffect(tag: "serviceCall") {
                guard case .ctor(_, _, let origin?) = svc else {
                    throw MarRuntimeError.message("Service.call: anonymous service has no path")
                }
                let path = "/services/\(origin.module).\(origin.name)"
                let bodyAny = MarJSONCodec.marToJSON(req)
                let bodyData = try? JSONSerialization.data(
                    withJSONObject: bodyAny,
                    options: [.fragmentsAllowed]
                )
                let bodyString = bodyData.flatMap { String(data: $0, encoding: .utf8) } ?? "null"
                MarHTTP.fireService(path: path, body: bodyString, toMsg: toMsg)
                return .unit
            })
        }
        env.define("serviceCall",  .fn(serviceCall))
        env.define("Service.call", .fn(serviceCall))

        // MARK: Http.get / Http.post
        let httpGet = MarFn.native(2) { args in
            let url = asString(args[0])
            let toMsg = args[1]
            return .effect(MarEffect(tag: "httpGet") {
                MarHTTP.fire(method: "GET", url: url, body: nil, toMsg: toMsg)
                return .unit
            })
        }
        env.define("httpGet",  .fn(httpGet))
        env.define("Http.get", .fn(httpGet))

        let httpPost = MarFn.native(3) { args in
            let url = asString(args[0])
            let body = asString(args[1])
            let toMsg = args[2]
            return .effect(MarEffect(tag: "httpPost") {
                MarHTTP.fire(method: "POST", url: url, body: body, toMsg: toMsg)
                return .unit
            })
        }
        env.define("httpPost",  .fn(httpPost))
        env.define("Http.post", .fn(httpPost))

        // MARK: Auth — passwordless email-code

        // Auth.config: server-side this captures the user entity +
        // signup hook into a global. iOS-side we additionally pull the
        // `signInPage` field's path off — Page.protected reads it as
        // the redirect target when the user has no session. Stashed
        // into AppContext so StackShell can read it at render time.
        let authConfig = MarFn.native(1) { args in
            if case .record(let fs, _) = args[0],
               let pageVal = fs["signInPage"],
               case .ctor(_, let pageArgs, _) = pageVal,
               let firstArg = pageArgs.first,
               case .string(let path) = firstArg {
                MainActor.assumeIsolated {
                    AppContext.shared.signInPath = path
                }
            }
            return .ctor(tag: "__Auth", args: [args[0]], origin: nil)
        }
        env.define("authConfig",  .fn(authConfig))
        env.define("Auth.config", .fn(authConfig))

        // Auth.protect : Service -> (req -> user -> Effect) -> ExposedService
        // Server-side wraps the handler with a session-validating
        // middleware. iOS-side, just returns the contract — handlers
        // run on the server, never on the device.
        let authProtect = MarFn.native(2) { args in args[0] }
        env.define("authProtect",  .fn(authProtect))
        env.define("Auth.protect", .fn(authProtect))

        // PROPOSAL stubs (see docs/authorization-proposal.md).
        // Authorization always runs server-side; iOS just returns
        // the contract unchanged so the call chain evaluates.
        let authRequireRole = MarFn.native(2) { args in args[1] }
        env.define("authRequireRole",  .fn(authRequireRole))
        env.define("Auth.requireRole", .fn(authRequireRole))

        let authAuthorize = MarFn.native(3) { args in args[2] }
        env.define("authAuthorize",  .fn(authAuthorize))
        env.define("Auth.authorize", .fn(authAuthorize))

        let authRequireOwner = MarFn.native(3) { args in args[2] }
        env.define("authRequireOwner",  .fn(authRequireOwner))
        env.define("Auth.requireOwner", .fn(authRequireOwner))

        // Auth.requestCode : { email } -> (Result String () -> msg) -> Effect
        let authRequestCode = MarFn.native(2) { args in
            let req = args[0]
            let toMsg = args[1]
            return .effect(MarEffect(tag: "Auth.requestCode") {
                MarHTTP.fireAuth(path: "/_auth/request-code",
                                  body: req,
                                  decode: .ackUnit,
                                  toMsg: toMsg)
                return .unit
            })
        }
        env.define("authRequestCode",  .fn(authRequestCode))
        env.define("Auth.requestCode", .fn(authRequestCode))

        // Auth.verifyCode : { email, code } -> (Result String User -> msg) -> Effect
        let authVerifyCode = MarFn.native(2) { args in
            let req = args[0]
            let toMsg = args[1]
            return .effect(MarEffect(tag: "Auth.verifyCode") {
                MarHTTP.fireAuth(path: "/_auth/verify-code",
                                  body: req,
                                  decode: .userJSON,
                                  toMsg: toMsg)
                return .unit
            })
        }
        env.define("authVerifyCode",  .fn(authVerifyCode))
        env.define("Auth.verifyCode", .fn(authVerifyCode))

        // Auth.logout : (Result String () -> msg) -> Effect
        let authLogout = MarFn.native(1) { args in
            let toMsg = args[0]
            return .effect(MarEffect(tag: "Auth.logout") {
                MarHTTP.fireAuth(path: "/_auth/logout",
                                  body: nil,
                                  decode: .ackUnit,
                                  toMsg: toMsg)
                return .unit
            })
        }
        env.define("authLogout",  .fn(authLogout))
        env.define("Auth.logout", .fn(authLogout))

        // Auth.me : (Result String (Maybe User) -> msg) -> Effect
        // GET semantics, `null` body → Nothing, JSON object → Just user.
        let authMe = MarFn.native(1) { args in
            let toMsg = args[0]
            return .effect(MarEffect(tag: "Auth.me") {
                MarHTTP.fireAuthMe(toMsg: toMsg)
                return .unit
            })
        }
        env.define("authMe",  .fn(authMe))
        env.define("Auth.me", .fn(authMe))

        return env
    }

    // MARK: View builtins

    private static func registerViewBuiltins(_ env: Env) {
        // Helper to read attrs out of a `List Attr` argument. Each Attr
        // is a record { name: String, value: <opaque> } produced by
        // the modifier builtins below.
        func collectAttrs(_ list: MarValue) -> [MarView.Attr] {
            guard case .list(let xs) = list else { return [] }
            var out: [MarView.Attr] = []
            for x in xs {
                if case .record(let fs, _) = x,
                   case .string(let name) = fs["name"] ?? .unit {
                    out.append(MarView.Attr(name: name, value: fs["value"] ?? .unit))
                }
            }
            return out
        }

        func childrenList(_ list: MarValue) -> [MarView] {
            guard case .list(let xs) = list else { return [] }
            return xs.compactMap {
                if case .view(let v) = $0 { return v } else { return nil }
            }
        }

        // 2-arg native: (List Attr, List View) -> View. Used by every
        // UI.* container that accepts both a modifiers list and a
        // children list (navigationStack / uiSection / hstack / vstack).
        func container(_ tag: String) -> MarFn {
            MarFn.native(2) { args in
                .view(MarView(
                    tag: tag,
                    attrs: collectAttrs(args[0]),
                    children: childrenList(args[1]),
                    text: "",
                    msg: nil,
                    key: nil
                ))
            }
        }

        // Shared helper: builds a `{name, value}` record — the runtime
        // shape of an Attr. Used by submit / input-kind attrs below
        // and by the UI.* container modifiers further down.
        func makeAttr(_ name: String, _ value: MarValue) -> MarValue {
            .record(
                fields: ["name": .string(name), "value": value],
                order: ["name", "value"]
            )
        }
        let flagAttr: (String) -> MarValue = { name in makeAttr(name, .unit) }

        // UI.submit : msg -> Attr — declarative event hookup. The
        // renderer reads this attr and wires it to SwiftUI's
        // `.onSubmit` modifier (Return/Done/Go on the keyboard).
        let viewSubmit = MarFn.native(1) { args in makeAttr("submit", args[0]) }
        env.define("viewSubmit", .fn(viewSubmit))

        // Input-kind attrs (UI.email / .password / .newPassword /
        // .numeric / .oneTimeCode). MarRenderer translates these into
        // SwiftUI modifiers — `.keyboardType`, `.textContentType`,
        // `.autocapitalization`, etc. — so iOS keyboards and Keychain
        // behave the same way Safari/Chrome do on the web.
        env.define("viewEmail",       flagAttr("inputKindEmail"))
        env.define("viewPassword",    flagAttr("inputKindPassword"))
        env.define("viewNewPassword", flagAttr("inputKindNewPassword"))
        env.define("viewNumeric",     flagAttr("inputKindNumeric"))
        // iOS-only: enables the "Code from Mail" / SMS one-time-code
        // suggestion above the keyboard. Composes with `numeric` so a
        // 6-digit code field uses both: `[ numeric, oneTimeCode ]`.
        // The renderer (MarRenderer.swift) maps this to
        // `.textContentType = .oneTimeCode`.
        env.define("viewOneTimeCode", flagAttr("inputKindOneTimeCode"))

        // ---------- UI module: SwiftUI-style declarative vocabulary ----------
        //
        // Mirror of the Go runtime's UI builtins. Same VView tags as
        // the JS runtime emits ("navigationStack", "form", "uiList",
        // "uiSection", "hstack", "vstack", "textField"). The iOS
        // renderer (MarRenderer.swift) recognizes these tags and
        // produces real SwiftUI primitives — NavigationStack, Form,
        // List, Section, HStack/VStack, TextField — with platform
        // chrome (safe areas, swipe-back, table styling) for free.

        // Helper: 1-arg container that takes only children (no attrs).
        func contentOnlyContainer(_ tag: String) -> MarFn {
            MarFn.native(1) { args in
                .view(MarView(
                    tag: tag,
                    attrs: [],
                    children: childrenList(args[0]),
                    text: "",
                    msg: nil,
                    key: nil
                ))
            }
        }

        // Containers
        env.define("navigationStack",    .fn(container("navigationStack")))
        env.define("UI.navigationStack", .fn(container("navigationStack")))
        env.define("form",        .fn(contentOnlyContainer("form")))
        env.define("UI.form",     .fn(contentOnlyContainer("form")))
        env.define("list",        .fn(contentOnlyContainer("uiList")))
        env.define("UI.list",     .fn(contentOnlyContainer("uiList")))
        env.define("uiSection",   .fn(container("uiSection")))
        env.define("UI.section",  .fn(container("uiSection")))
        env.define("hstack",      .fn(container("hstack")))
        env.define("UI.hstack",   .fn(container("hstack")))
        env.define("vstack",      .fn(container("vstack")))
        env.define("UI.vstack",   .fn(container("vstack")))

        // textField : List Attr -> String placeholder -> String value -> (String -> msg) -> View msg
        let uiTextField = MarFn.native(4) { args in
            var attrs = collectAttrs(args[0])
            attrs.append(MarView.Attr(name: "placeholder", value: args[1]))
            let value: String
            if case .string(let s) = args[2] { value = s } else { value = "" }
            return .view(MarView(
                tag: "textField",
                attrs: attrs,
                children: [],
                text: value,
                msg: args[3],
                key: nil
            ))
        }
        env.define("textField",    .fn(uiTextField))
        env.define("UI.textField", .fn(uiTextField))

        // text / button — leaf views without attrs.
        let uiText = MarFn.native(1) { args in
            let s: String
            if case .string(let str) = args[0] { s = str } else { s = "" }
            return .view(MarView(
                tag: "text", attrs: [], children: [], text: s, msg: nil, key: nil
            ))
        }
        env.define("uiText",  .fn(uiText))
        env.define("UI.text", .fn(uiText))

        // UI.button : List Attr -> msg -> String -> View msg
        let uiButton = MarFn.native(3) { args in
            let label: String
            if case .string(let s) = args[2] { label = s } else { label = "" }
            return .view(MarView(
                tag: "button",
                attrs: collectAttrs(args[0]),
                children: [],
                text: label,
                msg: args[1],
                key: nil
            ))
        }
        env.define("uiButton",  .fn(uiButton))
        env.define("UI.button", .fn(uiButton))

        // UI.disabled : Bool -> Attr — greys out an interactive view
        // (today: button) and suppresses dispatch. Symmetric for
        // true/false so user code can pass a derived Bool without
        // building the attrs list conditionally.
        let uiDisabled = MarFn.native(1) { args in
            makeAttr("disabled", args[0])
        }
        env.define("uiDisabled",  .fn(uiDisabled))
        env.define("UI.disabled", .fn(uiDisabled))

        // title / subtitle — heading + secondary heading. Reuse the
        // existing "title" / "subtitle" tags so the renderer's
        // existing branches apply (.font(.title2) / .font(.headline)).
        let uiTitle = MarFn.native(1) { args in
            let s: String
            if case .string(let str) = args[0] { s = str } else { s = "" }
            return .view(MarView(tag: "title", attrs: [], children: [], text: s, msg: nil, key: nil))
        }
        env.define("uiTitle",  .fn(uiTitle))
        env.define("UI.title", .fn(uiTitle))

        let uiSubtitle = MarFn.native(1) { args in
            let s: String
            if case .string(let str) = args[0] { s = str } else { s = "" }
            return .view(MarView(tag: "subtitle", attrs: [], children: [], text: s, msg: nil, key: nil))
        }
        env.define("uiSubtitle",  .fn(uiSubtitle))
        env.define("UI.subtitle", .fn(uiSubtitle))

        // link : Path r -> r -> String -> View msg
        // Builds the URL via the same path-pattern machinery as
        // linkTo, then emits a "link" view with `href` attr. iOS
        // renderer turns this into a NavigationLink / tappable
        // text that drives Nav.pushTo.
        let uiLink = MarFn.native(3) { args in
            guard case .string(let src) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Path", got: Eval.typeOf(args[0]))
            }
            let pattern = MarPath.parse(src)
            let url = try MarPath.build(pattern, params: args[1])
            let label: String
            if case .string(let s) = args[2] { label = s } else { label = "" }
            return .view(MarView(
                tag: "link",
                attrs: [MarView.Attr(name: "href", value: .string(url))],
                children: [],
                text: label,
                msg: nil,
                key: nil
            ))
        }
        env.define("uiLink",  .fn(uiLink))
        env.define("UI.link", .fn(uiLink))

        // empty : View msg — no-op placeholder (renders as
        // EmptyView / display:none).
        let uiEmptyView = MarValue.view(MarView(
            tag: "empty", attrs: [], children: [], text: "", msg: nil, key: nil
        ))
        env.define("uiEmpty",  uiEmptyView)
        env.define("UI.empty", uiEmptyView)

        // centered : View msg -> View msg
        // Wraps the child in a "centered" view tag — renderer maps
        // to .frame(maxWidth: .infinity, maxHeight: .infinity,
        // alignment: .center) on iOS, flex-center on web.
        let uiCentered = MarFn.native(1) { args in
            guard case .view(let child) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "View", got: Eval.typeOf(args[0]))
            }
            return .view(MarView(
                tag: "centered",
                attrs: [],
                children: [child],
                text: "",
                msg: nil,
                key: nil
            ))
        }
        env.define("uiCentered",  .fn(uiCentered))
        env.define("UI.centered", .fn(uiCentered))

        // Modifier attrs — produce VAttr values consumed by the
        // matching container's renderer.
        let navTitleCtor = MarFn.native(1) { args in makeAttr("navigationTitle", args[0]) }
        env.define("navigationTitle",    .fn(navTitleCtor))
        env.define("UI.navigationTitle", .fn(navTitleCtor))

        let trailingCtor = MarFn.native(1) { args in makeAttr("trailing", args[0]) }
        env.define("trailing",    .fn(trailingCtor))
        env.define("UI.trailing", .fn(trailingCtor))

        let leadingCtor = MarFn.native(1) { args in makeAttr("leading", args[0]) }
        env.define("leading",    .fn(leadingCtor))
        env.define("UI.leading", .fn(leadingCtor))

        let headerCtor = MarFn.native(1) { args in makeAttr("header", args[0]) }
        env.define("header",    .fn(headerCtor))
        env.define("UI.header", .fn(headerCtor))

        let footerCtor = MarFn.native(1) { args in makeAttr("footer", args[0]) }
        env.define("footer",    .fn(footerCtor))
        env.define("UI.footer", .fn(footerCtor))

        // numericCode bundles `numeric` + `oneTimeCode` as a single
        // flag — common OTP / 2FA case. Renderer expands it.
        env.define("numericCode",    flagAttr("inputKindNumericCode"))
        env.define("UI.numericCode", flagAttr("inputKindNumericCode"))

        // Re-expose existing input-kind / submit attrs under UI.*
        // so user code that lives entirely in the SwiftUI-style
        // vocabulary doesn't need a separate `import View`.
        env.define("UI.email",       flagAttr("inputKindEmail"))
        env.define("UI.password",    flagAttr("inputKindPassword"))
        env.define("UI.newPassword", flagAttr("inputKindNewPassword"))
        env.define("UI.numeric",     flagAttr("inputKindNumeric"))
        env.define("UI.oneTimeCode", flagAttr("inputKindOneTimeCode"))
        env.define("UI.submit",      .fn(viewSubmit))
    }

    // MARK: - Coercion helpers

    static func asInt(_ v: MarValue) -> Int {
        switch v {
        case .int(let n): return n
        case .float(let f): return Int(f)
        default: return 0
        }
    }
    static func asBool(_ v: MarValue) -> Bool {
        if case .bool(let b) = v { return b }
        return false
    }
    static func asString(_ v: MarValue) -> String {
        if case .string(let s) = v { return s }
        return ""
    }
}

