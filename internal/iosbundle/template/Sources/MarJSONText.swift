// JSON text on the phone: how a value is written (jsonText, quoteJSON) and how
// a document is read before JSONSerialization sees it (parse,
// wellFormedEscapes, isJSONNumber). The rules are the other two runtimes':
// internal/runtime/json.go and internal/jsserve/runtime.js.

import Foundation

extension MarJSONCodec {
    /// The JSON text of a value, byte for byte what the other two runtimes
    /// write (appendValue in internal/runtime/json.go, and JSON.stringify of
    /// marToJs in runtime.js): object keys in code point order, strings as
    /// quoteJSON writes them. Every place the phone turns a value into JSON
    /// text goes through here. It is written out rather than left to
    /// JSONSerialization, whose `.sortedKeys` orders keys numerically and
    /// without case (`a2` before `a10`) and which writes `/` as `\/`.
    static func jsonText(_ v: MarValue) throws -> String {
        var out = ""
        try writeJSON(marToJSON(v), into: &out)
        return out
    }

    private static func writeJSON(_ any: Any, into out: inout String) throws {
        switch any {
        case is NSNull:
            out += "null"
        case let b as Bool where type(of: any) == Bool.self:
            out += b ? "true" : "false"
        case let n as Int:
            out += String(n)
        case let s as String:
            quoteJSON(s, into: &out)
        case let xs as [Any]:
            out += "["
            for (i, x) in xs.enumerated() {
                if i > 0 { out += "," }
                try writeJSON(x, into: &out)
            }
            out += "]"
        case let d as [String: Any]:
            out += "{"
            let keys = d.keys.sorted { a, b in
                a.unicodeScalars.lexicographicallyPrecedes(b.unicodeScalars) { $0.value < $1.value }
            }
            for (i, k) in keys.enumerated() {
                if i > 0 { out += "," }
                quoteJSON(k, into: &out)
                out += ":"
                try writeJSON(d[k] ?? NSNull(), into: &out)
            }
            out += "}"
        default:
            throw MarRuntimeError.message("JSON.encode failed: the value cannot be written as JSON")
        }
    }

    /// A string as JavaScript's JSON.stringify writes it, as appendJSONString
    /// does on the server: `"` and `\` escaped, backspace, form feed, newline,
    /// carriage return and tab by name, every other scalar below U+0020 as
    /// \u00xx in lowercase hex, and everything else as itself.
    static func quoteJSON(_ s: String, into out: inout String) {
        out += "\""
        for u in s.unicodeScalars {
            switch u.value {
            case 0x22: out += "\\\""
            case 0x5C: out += "\\\\"
            case 0x08: out += "\\b"
            case 0x0C: out += "\\f"
            case 0x0A: out += "\\n"
            case 0x0D: out += "\\r"
            case 0x09: out += "\\t"
            case 0..<0x20: out += String(format: "\\u%04x", u.value)
            default: out.unicodeScalars.append(u)
            }
        }
        out += "\""
    }

    /// Parses JSON text into Foundation values the way every runtime reads a
    /// document: an escape of a surrogate that is not half of a pair
    /// (`\ud800` alone) stands for U+FFFD, as it does in the server's decoder
    /// and in the browser's (wellFormed in runtime.js). JSONSerialization
    /// refuses the whole document instead, so the escapes are rewritten first.
    /// Every place the phone reads a value from JSON goes through here.
    static func parse(_ data: Data) throws -> Any {
        try JSONSerialization.jsonObject(with: wellFormedEscapes(data), options: [.fragmentsAllowed])
    }

    /// The JSON text with each `\uXXXX` escape of an unpaired surrogate
    /// replaced by `\ufffd`, the same length. Valid UTF-8 cannot carry a raw
    /// surrogate, so an escape is the only way a document spells one.
    static func wellFormedEscapes(_ data: Data) -> Data {
        var b = [UInt8](data)
        func hex4(_ at: Int) -> Int? {
            guard at + 6 <= b.count, b[at] == 0x5C, b[at + 1] == 0x75 else { return nil }
            var v = 0
            for k in (at + 2)..<(at + 6) {
                guard let d = Int(String(UnicodeScalar(b[k])), radix: 16) else { return nil }
                v = v * 16 + d
            }
            return v
        }
        var changed = false
        var i = 0
        while i + 1 < b.count {
            guard b[i] == 0x5C else { i += 1; continue }
            guard let u = hex4(i) else { i += 2; continue }  // \" \\ \n and the like
            if (0xD800...0xDBFF).contains(u), let low = hex4(i + 6), (0xDC00...0xDFFF).contains(low) {
                i += 12
                continue
            }
            if (0xD800...0xDFFF).contains(u) {
                b.replaceSubrange((i + 2)..<(i + 6), with: Array("fffd".utf8))
                changed = true
            }
            i += 6
        }
        return changed ? Data(b) : data
    }

    /// Whether a lifted literal follows the JSON number grammar (RFC 8259):
    /// `-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?`. One that does not was
    /// never a number, and the document is not JSON. JSON_NUMBER in
    /// runtime.js is the same grammar.
    static func isJSONNumber(_ lit: String) -> Bool {
        let b = Array(lit.utf8)
        var i = 0
        func scanDigits() -> Int {
            let start = i
            while i < b.count, b[i] >= 0x30, b[i] <= 0x39 { i += 1 }
            return i - start
        }
        if i < b.count, b[i] == 0x2D { i += 1 }
        guard i < b.count else { return false }
        if b[i] == 0x30 {
            i += 1
        } else if scanDigits() == 0 {
            return false
        }
        if i < b.count, b[i] == 0x2E {
            i += 1
            guard scanDigits() > 0 else { return false }
        }
        if i < b.count, b[i] == 0x65 || b[i] == 0x45 {
            i += 1
            if i < b.count, b[i] == 0x2B || b[i] == 0x2D { i += 1 }
            guard scanDigits() > 0 else { return false }
        }
        return i == b.count
    }
}
