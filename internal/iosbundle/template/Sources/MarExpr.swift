// Expression + pattern AST — direct port of the kinds handled by
// `evalExpr` and `matchInto` in runtime.js. Decoded from the JSON
// shape that the Go server emits via `internal/jsserve.SerializeModule`.

import Foundation

indirect enum Expr {
    case int(Int)
    case float(Double)
    case string(String)
    /// Single Unicode code point. Wire format from Go side: EChar
    /// payload carries the int code point; we wrap it in Unicode.Scalar.
    case char(Unicode.Scalar)
    case unit

    case `var`(String)
    case ctor(module: [String], name: String)
    case qualified(module: [String], name: String)

    case negate(Expr)
    case app(fn: Expr, arg: Expr)
    case binop(op: String, left: Expr, right: Expr)
    case lambda(params: [Pat], body: Expr)
    case ifExpr(cond: Expr, then: Expr, else_: Expr)
    case letExpr(bindings: [LetBinding], body: Expr)

    case tuple([Expr])
    case list([Expr])
    case record([RecordField])
    case recordUpdate(record: Expr, fields: [RecordField])
    case fieldAccess(record: Expr, field: String)
    case fieldAccessor(field: String)

    case caseExpr(subject: Expr, branches: [CaseBranch])
}

struct LetBinding {
    let pattern: Pat
    let body: Expr
}

struct CaseBranch {
    let pattern: Pat
    let body: Expr
}

struct RecordField {
    let name: String
    let value: Expr
}

indirect enum Pat {
    case wildcard
    case `var`(String)
    case int(Int)
    case string(String)
    case char(Unicode.Scalar)
    case unit
    case ctor(name: String, args: [Pat])
    case tuple([Pat])
    case list([Pat])
    case cons(head: Pat, tail: Pat)
    /// `{ f1, f2, ... }` — partial record destructure. Binds each
    /// listed field name as a local. Extra fields on the matched
    /// value are ignored (Elm-style row-poly partial match).
    case record(fields: [String])
}

// MARK: - Module + declarations

struct Module {
    let name: [String]
    let imports: [Import]
    let decls: [Decl]
}

struct Import {
    let module: [String]
    /// Names re-exposed unqualified by `import M exposing (a, b)`.
    /// Empty list = no exposed names (just qualified access through
    /// `M.x`).
    let exposing: [String]
    /// `import M exposing (..)`: bring EVERY export of M into the
    /// bare namespace. Resolved by MarLoader against the env's
    /// `M.x` qualified bindings.
    let all: Bool
}

enum Decl {
    case value(name: String, params: [Pat], body: Expr)
    case customType(name: String, constructors: [CtorDecl])
    /// Anything we don't model yet (TypeAliasDecl, etc.) survives as
    /// an opaque marker so we don't choke on programs that compile.
    case other
}

struct CtorDecl {
    let name: String
    let argCount: Int
}

struct Program {
    /// Modules in load order (the server topo-sorts them so each
    /// module's imports are resolved before its body evaluates).
    /// Older bundles sent a single `module`; the decoder collapses
    /// that into a one-element list so this stays the only field
    /// the loader has to know about.
    let modules: [Module]
    let entry: String

    /// Sign-in path declared in `Auth.config { signInPage = ... }`,
    /// resolved by the server and shipped here. Page.protected reads
    /// this when the user has no session. Empty when the app has no
    /// Auth.config at all.
    let authSignInPath: String
}
