import Foundation
import SwiftUI

enum FrontendRuntimeError: LocalizedError {
    case message(String)

    var errorDescription: String? {
        switch self {
        case .message(let value):
            return value
        }
    }
}

struct FrontendTransition {
    let model: JSONValue
    let effects: [FrontendLocalEffect]
}

enum FrontendLocalEffect: Hashable {
    case back
    case go(target: String, arguments: [JSONValue])
    case command(operation: String, success: String, failure: String)
}

struct FrontendCommandRequest {
    let method: String
    let path: String
    let body: JSONValue?
}

struct FrontendRuntimeContext {
    let row: Row?
    let parameters: Row?
    let currentUserID: String?
    let currentUserEmail: String?
    let currentUserRole: String?
    let isAuthenticated: Bool
}

private enum FrontendLocalValue: Hashable {
    case null
    case string(String)
    case bool(Bool)
    case number(Double)
    case object(Row)
    case list([FrontendLocalValue])
    case tagged(String, [FrontendLocalValue])
    case effect(FrontendLocalEffect)
}

private indirect enum FrontendSexpNode {
    case string(String)
    case number(String)
    case symbol(String)
    case list([FrontendSexpNode])
}

enum FrontendRuntime {
    static func initialize(
        schema: Schema,
        screen: FrontendScreenInfo,
        runtimeContext: FrontendRuntimeContext
    ) throws -> FrontendTransition {
        guard let raw = screen.initExpression?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty else {
            return FrontendTransition(model: .null, effects: [])
        }
        let context = screenContext(
            screen: screen,
            runtimeContext: runtimeContext
        )
        return try evaluateTransition(schema: schema, context: context, raw: raw)
    }

    static func update(
        schema: Schema,
        screen: FrontendScreenInfo,
        currentModel: JSONValue,
        message: String,
        runtimeContext: FrontendRuntimeContext
    ) throws -> FrontendTransition {
        guard let updateBody = screen.updateBody,
              let messageName = screen.updateMessage,
              let modelName = screen.updateModel
        else {
            return FrontendTransition(model: currentModel, effects: [])
        }

        var context = screenContext(
            screen: screen,
            runtimeContext: runtimeContext
        )
        context[frontendNormalizeSymbol(messageName)] = try evaluate(schema: schema, context: context, raw: message)
        context[frontendNormalizeSymbol(modelName)] = localValue(from: currentModel)
        return try evaluateTransition(schema: schema, context: context, raw: updateBody)
    }

    static func update(
        schema: Schema,
        screen: FrontendScreenInfo,
        currentModel: JSONValue,
        messageName: String,
        payload: JSONValue?,
        runtimeContext: FrontendRuntimeContext
    ) throws -> FrontendTransition {
        guard let updateBody = screen.updateBody,
              let updateMessage = screen.updateMessage,
              let modelName = screen.updateModel
        else {
            return FrontendTransition(model: currentModel, effects: [])
        }

        var context = screenContext(
            screen: screen,
            runtimeContext: runtimeContext
        )
        context[frontendNormalizeSymbol(updateMessage)] = runtimeMessageValue(
            screen: screen,
            name: messageName,
            payload: payload
        )
        context[frontendNormalizeSymbol(modelName)] = localValue(from: currentModel)
        return try evaluateTransition(schema: schema, context: context, raw: updateBody)
    }

    static func commandRequest(
        schema: Schema,
        screen: FrontendScreenInfo,
        currentModel: JSONValue,
        operation: String,
        runtimeContext: FrontendRuntimeContext
    ) throws -> FrontendCommandRequest {
        guard case .list(let items) = try parseOne(operation),
              let head = items.first,
              case .symbol(let rawName) = head
        else {
            throw FrontendRuntimeError.message("Command operation must be a list")
        }

        var context = screenContext(
            screen: screen,
            runtimeContext: runtimeContext
        )
        if let modelName = screen.updateModel {
            context[frontendNormalizeSymbol(modelName)] = localValue(from: currentModel)
        }

        let name = frontendNormalizeSymbol(rawName)
        let args = Array(items.dropFirst())

        switch name {
        case "create":
            return try createCommandRequest(schema: schema, context: context, args: args)
        case "update":
            return try updateCommandRequest(schema: schema, context: context, args: args)
        case "delete":
            return try deleteCommandRequest(schema: schema, context: context, args: args)
        default:
            if let query = schema.queries.first(where: { $0.name == name }) {
                guard args.count == query.parameters.count else {
                    throw FrontendRuntimeError.message("\(query.name) expects \(query.parameters.count) arguments")
                }
                let values = try args.map { try evaluate(schema: schema, context: context, node: $0) }
                return FrontendCommandRequest(
                    method: "GET",
                    path: queryPath(path: query.path, parameters: query.parameters, values: values),
                    body: nil
                )
            }

            if let action = schema.actions.first(where: { $0.name == name }) {
                guard let alias = schema.inputAliases.first(where: { $0.name == action.inputAlias }) else {
                    throw FrontendRuntimeError.message("Input alias not found: \(action.inputAlias)")
                }
                guard args.count == alias.fields.count else {
                    throw FrontendRuntimeError.message("\(action.name) expects \(alias.fields.count) arguments")
                }
                let values = try args.map { try evaluate(schema: schema, context: context, node: $0) }
                let body = Dictionary(uniqueKeysWithValues: zip(alias.fields, values).map { field, value in
                    (field.name, jsonValue(from: value))
                })
                return FrontendCommandRequest(method: "POST", path: action.path, body: .object(body))
            }

            throw FrontendRuntimeError.message("Unsupported command operation: \(rawName)")
        }
    }

    static func replyMessage(screen: FrontendScreenInfo, name: String, value: JSONValue) -> String {
        if messageArity(screen: screen, name: name) == 0 {
            return name
        }
        return "(\(name) \(jsonInline(value)))"
    }

    static func failureMessage(screen: FrontendScreenInfo, name: String, message: String) -> String {
        if messageArity(screen: screen, name: name) == 0 {
            return name
        }
        return "(\(name) \(jsonInline(.string(message))))"
    }

    static func inputMessage(_ name: String?, value: String) -> String? {
        guard let trimmed = name?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty else {
            return nil
        }
        return "(\(trimmed) \(jsonInline(.string(value))))"
    }

    static func toggleMessage(_ name: String?, value: Bool) -> String? {
        guard let trimmed = name?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty else {
            return nil
        }
        return "(\(trimmed) \(value ? "true" : "false"))"
    }

    static func modelBySetting(model: JSONValue, fieldName: String?, value: JSONValue) -> JSONValue {
        guard let rawName = fieldName?.trimmingCharacters(in: .whitespacesAndNewlines), !rawName.isEmpty else {
            return model
        }
        var object: Row
        if case .object(let values) = model {
            object = values
        } else {
            object = [:]
        }
        object[frontendNormalizeSymbol(rawName)] = value
        return .object(object)
    }

    static func boolCondition(_ raw: String?, model: JSONValue) -> Bool {
        let expression = raw?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !expression.isEmpty else { return true }
        do {
            let value = try evaluate(
                schema: Schema.emptyForFrontendEvaluation,
                context: ["model": localValue(from: model)],
                raw: expression
            )
            if case .bool(let bool) = value {
                return bool
            }
        } catch {
            return false
        }
        return false
    }

    static func fieldText(_ model: JSONValue, fieldName: String?) -> String {
        guard let fieldName = fieldName?.trimmingCharacters(in: .whitespacesAndNewlines), !fieldName.isEmpty else {
            return ""
        }
        guard case .object(let values) = model else {
            return ""
        }
        return values[frontendNormalizeSymbol(fieldName)]?.stringValue ?? ""
    }

    static func fieldBool(_ model: JSONValue, fieldName: String?) -> Bool {
        guard let fieldName = fieldName?.trimmingCharacters(in: .whitespacesAndNewlines), !fieldName.isEmpty else {
            return false
        }
        guard case .object(let values) = model else {
            return false
        }
        return values[frontendNormalizeSymbol(fieldName)]?.boolValue ?? false
    }

    static func fieldRows(_ model: JSONValue, fieldName: String?) -> [Row] {
        guard let fieldName = fieldName?.trimmingCharacters(in: .whitespacesAndNewlines), !fieldName.isEmpty else {
            return []
        }
        guard case .object(let values) = model,
              case .array(let items)? = values[frontendNormalizeSymbol(fieldName)]
        else {
            return []
        }
        return items.compactMap { item in
            if case .object(let row) = item {
                return row
            }
            return nil
        }
    }
}

private func runtimeMessageValue(screen: FrontendScreenInfo, name: String, payload: JSONValue?) -> FrontendLocalValue {
    let normalizedName = frontendNormalizeSymbol(name)
    guard messageArity(screen: screen, name: normalizedName) > 0, let payload else {
        return .tagged(normalizedName, [])
    }
    return .tagged(normalizedName, [localValue(from: payload)])
}

private extension Schema {
    static var emptyForFrontendEvaluation: Schema {
        Schema(
            appName: "",
            port: 0,
            database: "",
            entities: [],
            auth: nil,
            systemAuth: nil,
            inputAliases: [],
            actions: [],
            queries: [],
            records: [],
            screens: nil
        )
    }
}

private func evaluateTransition(schema: Schema, context: [String: FrontendLocalValue], raw: String) throws -> FrontendTransition {
    let value = try evaluate(schema: schema, context: context, raw: raw)
    guard case .list(let values) = value,
          values.count == 2,
          case .list(let effects) = values[1]
    else {
        throw FrontendRuntimeError.message("Screen transition must return (model effects)")
    }
    return FrontendTransition(
        model: jsonValue(from: values[0]),
        effects: effects.compactMap { value in
            if case .effect(let effect) = value {
                return effect
            }
            return nil
        }
    )
}

private func screenContext(
    screen: FrontendScreenInfo,
    runtimeContext: FrontendRuntimeContext
) -> [String: FrontendLocalValue] {
    let currentUser: FrontendLocalValue = runtimeContext.currentUserID
        .flatMap(Double.init)
        .map {
            .tagged("authenticated", [
                .number($0),
                .string(runtimeContext.currentUserEmail ?? ""),
                .string(runtimeContext.currentUserRole ?? "")
            ])
        } ?? .tagged("anonymous", [])
    var context: [String: FrontendLocalValue] = [
        "current_user": currentUser
    ]

    if let firstParameter = screen.parameters.first, let row = runtimeContext.row {
        context[frontendNormalizeSymbol(firstParameter)] = .object(row)
    }
    if let parameters = runtimeContext.parameters {
        for (key, value) in parameters {
            context[frontendNormalizeSymbol(key)] = localValue(from: value)
        }
    }
    return context
}

private func evaluate(schema: Schema, context: [String: FrontendLocalValue], raw: String) throws -> FrontendLocalValue {
    try evaluate(schema: schema, context: context, node: parseOne(raw))
}

private func evaluate(schema: Schema, context: [String: FrontendLocalValue], node: FrontendSexpNode) throws -> FrontendLocalValue {
    switch node {
    case .string(let value):
        return .string(value)
    case .number(let raw):
        return .number(Double(raw) ?? 0)
    case .symbol(let value):
        return try resolveSymbol(context: context, symbol: value)
    case .list(let items):
        return try evaluateList(schema: schema, context: context, items: items)
    }
}

private func evaluateList(schema: Schema, context: [String: FrontendLocalValue], items: [FrontendSexpNode]) throws -> FrontendLocalValue {
    guard let head = items.first else {
        return .list([])
    }
    guard case .symbol(let rawHead) = head else {
        return .list(try items.map { try evaluate(schema: schema, context: context, node: $0) })
    }

    let args = Array(items.dropFirst())
    let normalizedHead = frontendNormalizeSymbol(rawHead)

    if let coreValue = try evaluateCoreForm(schema: schema, context: context, name: normalizedHead, args: args) {
        return coreValue
    }
    if let comparisonValue = try evaluateComparisonForm(schema: schema, context: context, name: normalizedHead, args: args) {
        return comparisonValue
    }
    return try evaluateApplication(schema: schema, context: context, name: normalizedHead, items: items, args: args)
}

private func evaluateCoreForm(
    schema: Schema,
    context: [String: FrontendLocalValue],
    name: String,
    args: [FrontendSexpNode]
) throws -> FrontendLocalValue? {
    if let value = try evaluateControlForm(schema: schema, context: context, name: name, args: args) {
        return value
    }
    if let value = try evaluateEffectForm(schema: schema, context: context, name: name, args: args) {
        return value
    }
    return try evaluateBooleanForm(schema: schema, context: context, name: name, args: args)
}

private func evaluateControlForm(
    schema: Schema,
    context: [String: FrontendLocalValue],
    name: String,
    args: [FrontendSexpNode]
) throws -> FrontendLocalValue? {
    switch name {
    case "if":
        guard args.count == 3 else { throw FrontendRuntimeError.message("if expects 3 arguments") }
        let condition = try requireBool(evaluate(schema: schema, context: context, node: args[0]))
        return try evaluate(schema: schema, context: context, node: condition ? args[1] : args[2])
    case "match":
        guard let subjectRaw = args.first else { throw FrontendRuntimeError.message("match expects a subject and clauses") }
        let subject = try evaluate(schema: schema, context: context, node: subjectRaw)
        return try evaluateMatch(schema: schema, context: context, subject: subject, clauses: Array(args.dropFirst()))
    case "get":
        guard args.count == 2, case .symbol(let fieldName) = args[1] else {
            throw FrontendRuntimeError.message("get expects a target and a field")
        }
        return try getField(fieldName, target: evaluate(schema: schema, context: context, node: args[0]))
    case "assoc":
        guard let targetRaw = args.first else { throw FrontendRuntimeError.message("assoc expects a target") }
        let target = try evaluate(schema: schema, context: context, node: targetRaw)
        return try assoc(schema: schema, context: context, target: target, updates: Array(args.dropFirst()))
    default:
        return nil
    }
}

private func evaluateEffectForm(
    schema: Schema,
    context: [String: FrontendLocalValue],
    name: String,
    args: [FrontendSexpNode]
) throws -> FrontendLocalValue? {
    switch name {
    case "command":
        guard args.count == 3, case .symbol(let success) = args[1], case .symbol(let failure) = args[2] else {
            throw FrontendRuntimeError.message("command expects an operation, success message, and failure message")
        }
        return .effect(.command(
            operation: inlineString(args[0]),
            success: frontendNormalizeSymbol(success),
            failure: frontendNormalizeSymbol(failure)
        ))
    case "go":
        guard let targetRaw = args.first, case .symbol(let targetName) = targetRaw else {
            throw FrontendRuntimeError.message("go expects a screen")
        }
        let values = try args.dropFirst().map {
            jsonValue(from: try evaluate(schema: schema, context: context, node: $0))
        }
        return .effect(.go(target: canonicalScreenName(targetName), arguments: values))
    case "back":
        guard args.isEmpty else { throw FrontendRuntimeError.message("back does not accept arguments") }
        return .effect(.back)
    default:
        return nil
    }
}

private func evaluateBooleanForm(
    schema: Schema,
    context: [String: FrontendLocalValue],
    name: String,
    args: [FrontendSexpNode]
) throws -> FrontendLocalValue? {
    switch name {
    case "not":
        guard args.count == 1 else { throw FrontendRuntimeError.message("not expects 1 argument") }
        return .bool(!(try requireBool(evaluate(schema: schema, context: context, node: args[0]))))
    case "and":
        return .bool(try args.allSatisfy { try requireBool(evaluate(schema: schema, context: context, node: $0)) })
    case "or":
        for arg in args where try requireBool(evaluate(schema: schema, context: context, node: arg)) {
            return .bool(true)
        }
        return .bool(false)
    case "authenticated?":
        guard args.count == 1 else { throw FrontendRuntimeError.message("authenticated? expects 1 argument") }
        return try authenticatedPredicate(evaluate(schema: schema, context: context, node: args[0]))
    case "anonymous?":
        guard args.count == 1 else { throw FrontendRuntimeError.message("anonymous? expects 1 argument") }
        return try anonymousPredicate(evaluate(schema: schema, context: context, node: args[0]))
    case "same-user?":
        guard args.count == 2 else { throw FrontendRuntimeError.message("same-user? expects 2 arguments") }
        return sameUserPredicate(
            currentUser: try evaluate(schema: schema, context: context, node: args[0]),
            candidate: try evaluate(schema: schema, context: context, node: args[1])
        )
    case "has-role?":
        guard args.count == 2 else { throw FrontendRuntimeError.message("has-role? expects 2 arguments") }
        return hasRolePredicate(
            currentUser: try evaluate(schema: schema, context: context, node: args[0]),
            candidate: try evaluate(schema: schema, context: context, node: args[1])
        )
    default:
        return nil
    }
}

private func evaluateComparisonForm(
    schema: Schema,
    context: [String: FrontendLocalValue],
    name: String,
    args: [FrontendSexpNode]
) throws -> FrontendLocalValue? {
    switch name {
    case "=":
        return try comparable(schema: schema, context: context, args: args) { $0 == $1 }
    case "!=":
        return try comparable(schema: schema, context: context, args: args) { $0 != $1 }
    case ">":
        return try numericComparable(schema: schema, context: context, args: args) { $0 > $1 }
    case ">=":
        return try numericComparable(schema: schema, context: context, args: args) { $0 >= $1 }
    case "<":
        return try numericComparable(schema: schema, context: context, args: args) { $0 < $1 }
    case "<=":
        return try numericComparable(schema: schema, context: context, args: args) { $0 <= $1 }
    default:
        return nil
    }
}

private func evaluateApplication(
    schema: Schema,
    context: [String: FrontendLocalValue],
    name: String,
    items: [FrontendSexpNode],
    args: [FrontendSexpNode]
) throws -> FrontendLocalValue {
    if let record = schema.records.first(where: { $0.name == name }) {
        guard args.count == record.fields.count else {
            throw FrontendRuntimeError.message("\(record.name) expects \(record.fields.count) arguments")
        }
        let values = try args.map { try evaluate(schema: schema, context: context, node: $0) }
        return .object(Dictionary(uniqueKeysWithValues: zip(record.fields, values).map { field, value in
            (field.name, jsonValue(from: value))
        }))
    }
    if context[name] != nil {
        return .list(try items.map { try evaluate(schema: schema, context: context, node: $0) })
    }
    return .tagged(name, try args.map { try evaluate(schema: schema, context: context, node: $0) })
}

private func resolveSymbol(context: [String: FrontendLocalValue], symbol: String) throws -> FrontendLocalValue {
    let normalized = frontendNormalizeSymbol(symbol)
    if let value = context[normalized] {
        return value
    }

    let parts = symbol.split(separator: ".").map(String.init)
    guard let root = parts.first, let rootValue = context[frontendNormalizeSymbol(root)] else {
        switch normalized {
        case "true": return .bool(true)
        case "false": return .bool(false)
        default: return .tagged(normalized, [])
        }
    }

    return try parts.dropFirst().reduce(rootValue) { value, fieldName in
        try getField(fieldName, target: value)
    }
}

private func getField(_ fieldName: String, target: FrontendLocalValue) throws -> FrontendLocalValue {
    guard case .object(let row) = target else {
        throw FrontendRuntimeError.message("get expects a record-like value")
    }
    let normalizedField = frontendNormalizeSymbol(fieldName)
    guard let value = row[normalizedField] else {
        throw FrontendRuntimeError.message("record has no field \"\(normalizedField)\"")
    }
    return localValue(from: value)
}

private func assoc(
    schema: Schema,
    context: [String: FrontendLocalValue],
    target: FrontendLocalValue,
    updates: [FrontendSexpNode]
) throws -> FrontendLocalValue {
    guard case .object(var row) = target else {
        throw FrontendRuntimeError.message("assoc expects a record-like value")
    }
    for update in updates {
        guard case .list(let pair) = update,
              pair.count == 2,
              case .symbol(let fieldName) = pair[0]
        else {
            throw FrontendRuntimeError.message("assoc updates must look like (field value)")
        }
        let normalizedField = frontendNormalizeSymbol(fieldName)
        guard row[normalizedField] != nil else {
            throw FrontendRuntimeError.message("record has no field \"\(normalizedField)\"")
        }
        row[normalizedField] = jsonValue(
            from: try evaluate(schema: schema, context: context, node: pair[1])
        )
    }
    return .object(row)
}

private func evaluateMatch(
    schema: Schema,
    context: [String: FrontendLocalValue],
    subject: FrontendLocalValue,
    clauses: [FrontendSexpNode]
) throws -> FrontendLocalValue {
    for clause in clauses {
        guard case .list(let parts) = clause, parts.count >= 2 else {
            throw FrontendRuntimeError.message("invalid match clause")
        }
        if let bindings = matchPattern(parts[0], subject: subject) {
            var child = context
            for (key, value) in bindings {
                child[key] = value
            }
            return try evaluate(schema: schema, context: child, node: parts[1])
        }
    }
    throw FrontendRuntimeError.message("match had no matching clause")
}

private func matchPattern(_ pattern: FrontendSexpNode, subject: FrontendLocalValue) -> [String: FrontendLocalValue]? {
    if case .list(let parts) = pattern,
       let first = parts.first,
       case .symbol(let rawTag) = first {
        let tag = frontendNormalizeSymbol(rawTag)
        let vars = Array(parts.dropFirst())

        if case .tagged(let subjectTag, let values) = subject,
           subjectTag == tag,
           values.count == vars.count {
            return matchBindings(vars: vars, values: values)
        }

        if tag == "just", vars.count == 1, !isNothing(subject) {
            return matchBindings(vars: vars, values: [subject])
        }
        return nil
    }

    if case .symbol(let rawTag) = pattern,
       case .tagged(let subjectTag, let values) = subject,
       frontendNormalizeSymbol(rawTag) == subjectTag,
       values.isEmpty {
        return [:]
    }
    return nil
}

private func matchBindings(vars: [FrontendSexpNode], values: [FrontendLocalValue]) -> [String: FrontendLocalValue]? {
    guard vars.count == values.count else { return nil }
    var result: [String: FrontendLocalValue] = [:]
    for (node, value) in zip(vars, values) {
        guard case .symbol(let name) = node else { return nil }
        result[frontendNormalizeSymbol(name)] = value
    }
    return result
}

private func comparable(
    schema: Schema,
    context: [String: FrontendLocalValue],
    args: [FrontendSexpNode],
    compare: (String, String) -> Bool
) throws -> FrontendLocalValue {
    guard args.count == 2 else { throw FrontendRuntimeError.message("comparison expects 2 arguments") }
    let left = comparableString(try evaluate(schema: schema, context: context, node: args[0]))
    let right = comparableString(try evaluate(schema: schema, context: context, node: args[1]))
    return .bool(compare(left, right))
}

private func numericComparable(
    schema: Schema,
    context: [String: FrontendLocalValue],
    args: [FrontendSexpNode],
    compare: (Double, Double) -> Bool
) throws -> FrontendLocalValue {
    guard args.count == 2 else { throw FrontendRuntimeError.message("comparison expects 2 arguments") }
    let left = try numberValue(evaluate(schema: schema, context: context, node: args[0]))
    let right = try numberValue(evaluate(schema: schema, context: context, node: args[1]))
    return .bool(compare(left, right))
}

private func requireBool(_ value: FrontendLocalValue) throws -> Bool {
    guard case .bool(let bool) = value else {
        throw FrontendRuntimeError.message("Expected Bool")
    }
    return bool
}

private func authenticatedPredicate(_ value: FrontendLocalValue) throws -> FrontendLocalValue {
    switch value {
    case .tagged("authenticated", let values) where values.count == 3:
        return .bool(true)
    case .tagged("anonymous", let values) where values.isEmpty:
        return .bool(false)
    default:
        throw FrontendRuntimeError.message("authenticated? expects current-user")
    }
}

private func anonymousPredicate(_ value: FrontendLocalValue) throws -> FrontendLocalValue {
    switch value {
    case .tagged("anonymous", let values) where values.isEmpty:
        return .bool(true)
    case .tagged("authenticated", let values) where values.count == 3:
        return .bool(false)
    default:
        throw FrontendRuntimeError.message("anonymous? expects current-user")
    }
}

private func sameUserPredicate(currentUser: FrontendLocalValue, candidate: FrontendLocalValue) -> FrontendLocalValue {
    switch currentUser {
    case .tagged("authenticated", let values) where values.count == 3:
        return .bool(comparableString(values[0]) == comparableString(candidate))
    default:
        return .bool(false)
    }
}

private func hasRolePredicate(currentUser: FrontendLocalValue, candidate: FrontendLocalValue) -> FrontendLocalValue {
    switch currentUser {
    case .tagged("authenticated", let values) where values.count == 3:
        return .bool(comparableString(values[2]) == comparableString(candidate))
    default:
        return .bool(false)
    }
}

private func numberValue(_ value: FrontendLocalValue) throws -> Double {
    switch value {
    case .number(let value):
        return value
    case .string(let value):
        if let number = Double(value) {
            return number
        }
        fallthrough
    default:
        throw FrontendRuntimeError.message("Expected Number")
    }
}

private func localValue(from value: JSONValue) -> FrontendLocalValue {
    switch value {
    case .string(let value):
        return .string(value)
    case .number(let value):
        return .number(value)
    case .bool(let value):
        return .bool(value)
    case .object(let row):
        if case .string(let tag)? = row["tag"],
           case .array(let values)? = row["values"] {
            return .tagged(tag, values.map(localValue(from:)))
        }
        return .object(row)
    case .array(let values):
        return .list(values.map(localValue(from:)))
    case .null:
        return .tagged("nothing", [])
    }
}

private func jsonValue(from value: FrontendLocalValue) -> JSONValue {
    switch value {
    case .null:
        return .null
    case .string(let value):
        return .string(value)
    case .number(let value):
        return .number(value)
    case .bool(let value):
        return .bool(value)
    case .object(let value):
        return .object(value)
    case .list(let values):
        return .array(values.map(jsonValue(from:)))
    case .tagged(let tag, let values):
        return .object(["tag": .string(tag), "values": .array(values.map(jsonValue(from:)))])
    case .effect(let effect):
        switch effect {
        case .back:
            return .object(["kind": .string("back")])
        case .go(let target, let arguments):
            return .object(["kind": .string("go"), "target": .string(target), "arguments": .array(arguments)])
        case .command(let operation, let success, let failure):
            return .object([
                "kind": .string("command"),
                "operation": .string(operation),
                "success": .string(success),
                "failure": .string(failure)
            ])
        }
    }
}

private func isNothing(_ value: FrontendLocalValue) -> Bool {
    if case .tagged(let tag, let values) = value {
        return tag == "nothing" && values.isEmpty
    }
    return false
}

private func comparableString(_ value: FrontendLocalValue) -> String {
    switch value {
    case .string(let value):
        return value
    case .number(let value):
        return value.rounded() == value ? String(Int(value)) : String(value)
    case .bool(let value):
        return value ? "true" : "false"
    case .null:
        return ""
    case .tagged(let tag, _):
        return tag
    default:
        return jsonInline(jsonValue(from: value))
    }
}

private func createCommandRequest(
    schema: Schema,
    context: [String: FrontendLocalValue],
    args: [FrontendSexpNode]
) throws -> FrontendCommandRequest {
    guard args.count == 2, case .symbol(let entityName) = args[0],
          let entity = schema.entities.first(where: { $0.name == canonicalScreenName(entityName) })
    else {
        throw FrontendRuntimeError.message("create expects an entity and values")
    }
    return FrontendCommandRequest(
        method: "POST",
        path: entity.resource,
        body: try commandValuesPayload(schema: schema, context: context, node: args[1])
    )
}

private func updateCommandRequest(
    schema: Schema,
    context: [String: FrontendLocalValue],
    args: [FrontendSexpNode]
) throws -> FrontendCommandRequest {
    guard args.count == 3, case .symbol(let entityName) = args[0],
          let entity = schema.entities.first(where: { $0.name == canonicalScreenName(entityName) })
    else {
        throw FrontendRuntimeError.message("update expects an entity, id, and values")
    }
    let id = comparableString(try evaluate(schema: schema, context: context, node: args[1]))
    return FrontendCommandRequest(
        method: "PATCH",
        path: entity.resource + "/" + id,
        body: try commandValuesPayload(schema: schema, context: context, node: args[2])
    )
}

private func deleteCommandRequest(
    schema: Schema,
    context: [String: FrontendLocalValue],
    args: [FrontendSexpNode]
) throws -> FrontendCommandRequest {
    guard args.count == 2, case .symbol(let entityName) = args[0],
          let entity = schema.entities.first(where: { $0.name == canonicalScreenName(entityName) })
    else {
        throw FrontendRuntimeError.message("delete expects an entity and id")
    }
    let id = comparableString(try evaluate(schema: schema, context: context, node: args[1]))
    return FrontendCommandRequest(method: "DELETE", path: entity.resource + "/" + id, body: nil)
}

private func commandValuesPayload(schema: Schema, context: [String: FrontendLocalValue], node: FrontendSexpNode) throws -> JSONValue {
    guard case .list(let pairs) = node else {
        throw FrontendRuntimeError.message("command values must be a list")
    }
    var body: Row = [:]
    for pair in pairs {
        guard case .list(let parts) = pair,
              parts.count == 2,
              case .symbol(let fieldName) = parts[0]
        else {
            throw FrontendRuntimeError.message("command values must look like ((field value) ...)")
        }
        body[frontendNormalizeSymbol(fieldName)] = jsonValue(from: try evaluate(schema: schema, context: context, node: parts[1]))
    }
    return .object(body)
}

private func queryPath(path: String, parameters: [String], values: [FrontendLocalValue]) -> String {
    let pairs = zip(parameters, values).map { name, value in
        let encodedName = name.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? name
        let rawValue = comparableString(value)
        let encodedValue = rawValue.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? rawValue
        return "\(encodedName)=\(encodedValue)"
    }
    return pairs.isEmpty ? path : path + "?" + pairs.joined(separator: "&")
}

private func messageArity(screen: FrontendScreenInfo, name: String) -> Int {
    screen.messages.first(where: { $0.name == name })?.parameters.count ?? 0
}

private func jsonInline(_ value: JSONValue) -> String {
    guard let data = try? JSONEncoder().encode(value),
          let encoded = String(data: data, encoding: .utf8)
    else {
        return "null"
    }
    return encoded
}

private func canonicalScreenName(_ value: String) -> String {
    value.split(separator: "-").map { part in
        guard let first = part.first else { return "" }
        return first.uppercased() + part.dropFirst()
    }.joined()
}

func frontendNormalizeSymbol(_ value: String) -> String {
    switch value {
    case "current-user":
        return "current_user"
    default:
        return value.replacingOccurrences(of: "-", with: "_")
    }
}

private func parseOne(_ raw: String) throws -> FrontendSexpNode {
    var parser = FrontendSexpParser(raw)
    return try parser.parseOne()
}

private func inlineString(_ node: FrontendSexpNode) -> String {
    switch node {
    case .string(let value):
        return jsonInline(.string(value))
    case .number(let value), .symbol(let value):
        return value
    case .list(let children):
        return "(" + children.map(inlineString).joined(separator: " ") + ")"
    }
}

private struct FrontendSexpParser {
    private let chars: [Character]
    private var index = 0

    init(_ raw: String) {
        chars = Array(raw)
    }

    mutating func parseOne() throws -> FrontendSexpNode {
        skipWhitespace()
        let node = try parseNode()
        skipWhitespace()
        guard index == chars.count else {
            throw FrontendRuntimeError.message("Expected exactly one expression")
        }
        return node
    }

    private mutating func parseNode() throws -> FrontendSexpNode {
        skipWhitespace()
        guard index < chars.count else {
            throw FrontendRuntimeError.message("Unexpected end of expression")
        }
        switch chars[index] {
        case "(":
            index += 1
            var children: [FrontendSexpNode] = []
            while true {
                skipWhitespace()
                guard index < chars.count else {
                    throw FrontendRuntimeError.message("Unterminated list")
                }
                if chars[index] == ")" {
                    index += 1
                    return .list(children)
                }
                children.append(try parseNode())
            }
        case "\"":
            return try parseString()
        default:
            return parseAtom()
        }
    }

    private mutating func parseString() throws -> FrontendSexpNode {
        var raw = "\""
        index += 1
        var escaped = false
        while index < chars.count {
            let char = chars[index]
            index += 1
            raw.append(char)
            if escaped {
                escaped = false
            } else if char == "\\" {
                escaped = true
            } else if char == "\"" {
                guard let data = raw.data(using: .utf8),
                      let decoded = try? JSONDecoder().decode(String.self, from: data)
                else {
                    throw FrontendRuntimeError.message("Invalid string literal")
                }
                return .string(decoded)
            }
        }
        throw FrontendRuntimeError.message("Unterminated string literal")
    }

    private mutating func parseAtom() -> FrontendSexpNode {
        var value = ""
        while index < chars.count {
            let char = chars[index]
            if char.isWhitespace || char == "(" || char == ")" {
                break
            }
            value.append(char)
            index += 1
        }
        return Double(value) == nil ? .symbol(value) : .number(value)
    }

    private mutating func skipWhitespace() {
        while index < chars.count, chars[index].isWhitespace {
            index += 1
        }
    }
}
