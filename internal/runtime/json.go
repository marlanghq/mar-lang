package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// jsonBuiltins returns runtime functions for JSON encode/decode of
// any Value to/from a JSON string. The mapping mirrors the codec rules
// in docs/mar.md.
//
//	JSON.encode  : a -> String
//	JSON.decode  : String -> Result String a   -- (decoded shape is "any", uses VRecord/VList/VBool/etc.)
func jsonBuiltins() map[string]Value {
	return map[string]Value{
		"jsonEncode": nativeFn(1, func(args []Value) (Value, error) {
			s, err := encodeValue(args[0])
			if err != nil {
				return nil, err
			}
			return VString{V: s}, nil
		}),
		"jsonDecode": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("JSON.decode: expected String")
			}
			v, err := decodeJSON(s.V)
			if err != nil {
				// Mar's sentence, not the host parser's. Every OTHER decode
				// failure here is already Mar's own ("missing the field `x`",
				// "should be a string, got a whole number") and reads the same
				// on all three runtimes; this one leaked whatever the host
				// said, so the same program answered differently on each —
				// Go's "invalid character '<'...", V8's "Unexpected token
				// '<'...", and on iOS a message that changes with the PHONE'S
				// LANGUAGE, because it came from localizedDescription.
				return VCtor{Tag: "Err", Args: []Value{VString{V: err.Error()}}}, nil
			}
			return VCtor{Tag: "Ok", Args: []Value{v}}, nil
		}),
	}
}

// encodeValue serializes a runtime Value to a JSON string.
//
// Encoding rules:
//   - VInt -> number
//   - VString -> string
//   - VBool -> true/false
//   - VUnit -> null  (only () encodes as bare null; the decoder side
//     reads null as VUnit unambiguously)
//   - VList -> array
//   - VRecord -> object
//   - Every VCtor, Nothing included, uses the {"__ctor":"Name"} marker
//     (with optional "__args"). Tagging Nothing like every other ctor
//     keeps it distinguishable from VUnit on the wire: a service
//     `Int -> ()` returns bare null, and `Ok ()` patterns must still
//     match it without colliding with `Nothing`.
//
// marKindOf names a value the way the program's author would: `a Page`, not
// `runtime.VPage`.
func marKindOf(v Value) string {
	switch v.(type) {
	case VPage:
		return "a Page"
	case VView:
		return "a View"
	case VEntity:
		return "an Entity"
	case VColumn:
		return "a Column"
	case VConstraint:
		return "a Constraint"
	case VService:
		return "a Service"
	case VExposedService:
		return "an exposed Service"
	case VAuth:
		return "an Auth config"
	case VPath:
		return "a Path"
	case VShared:
		return "a shared store"
	case VFn:
		return "a function"
	case VEffect:
		return "a Cmd or Task"

	// The ordinary kinds. They were missing, so the one place this function
	// exists to serve -- an argument the checker did not pin, where the value
	// really can be anything -- still answered "this value (runtime.VInt)",
	// with Go's name for a type the program's author never wrote.
	case VInt:
		return "a whole number"
	case VDecimal:
		return "a decimal"
	case VString:
		return "a string"
	case VChar:
		return "a character"
	case VBool:
		return "a true/false"
	case VUnit:
		return "()"
	case VRecord:
		return "a record"
	case VList:
		return "a list"
	case VTuple:
		return "a tuple"
	case VDict:
		return "a Dict"
	case VSet:
		return "a Set"
	case VTime:
		return "a Time"
	case VDuration:
		return "a Duration"
	}
	return fmt.Sprintf("this value (%T)", v)
}

func encodeValue(v Value) (string, error) {
	// One buffer for the whole value. This used to build a string per
	// nested value and copy it into its parent's builder, so a list of
	// records was copied once per level and allocated once per field; the
	// bytes are the same, they are just written once.
	buf, err := appendValue(make([]byte, 0, 64), v)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// appendJSONString writes s as a JSON string, byte for byte what JavaScript's
// JSON.stringify writes (ECMA-262 QuoteJSONString), which is what the browser
// writes and what the phone is configured to write: `"` and `\` escaped,
// backspace, form feed, newline, carriage return and tab by name, every other
// code point below U+0020 as \u00xx in lowercase hex, and everything else as
// itself, `/`, `<`, `>`, `&`, U+2028 and U+2029 included. A byte that is not
// part of valid UTF-8 is written as U+FFFD, one per byte.
//
// It is written out rather than left to encoding/json, which escapes `<`,
// `>`, `&`, U+2028 and U+2029 and whose handling of invalid UTF-8 depends on
// the toolchain's JSON implementation.
func appendJSONString(dst []byte, s string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	start := 0 // the run of bytes copied as they are
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' && c < utf8.RuneSelf {
			i++
			continue
		}
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r != utf8.RuneError || size != 1 {
				i += size
				continue
			}
			dst = append(dst, s[start:i]...)
			dst = utf8.AppendRune(dst, utf8.RuneError)
			i++
			start = i
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"':
			dst = append(dst, '\\', '"')
		case '\\':
			dst = append(dst, '\\', '\\')
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		}
		i++
		start = i
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

func appendValue(dst []byte, v Value) ([]byte, error) {
	switch x := v.(type) {
	case VInt:
		return strconv.AppendInt(dst, x.V, 10), nil
	case VString:
		return appendJSONString(dst, x.V), nil
	case VChar:
		// Wire format `{"__char": "x"}`, a 1-char string under the
		// marker. JSON itself has no Char type; this marker tells the
		// decoder side (Go's convertJSON / JS jsToMar / Swift
		// MarJSONCodec) to rebuild a VChar instead of degrading to a
		// 1-char VString.
		dst = append(dst, `{"__char":`...)
		dst = appendJSONString(dst, string(x.V))
		return append(dst, '}'), nil
	case VBool:
		if x.V {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case VDecimal:
		// Wire format `{"__dec": "1.50"}`, the value rides as a STRING
		// so no JSON parser on any side ever runs it through binary
		// floating point. The marker (same convention as __time/__char)
		// lets jsToMar / MarJSONCodec rebuild a Decimal instead of a
		// 4-char string; the digits themselves stay readable to non-mar
		// consumers inspecting raw JSON.
		dst = append(dst, `{"__dec":`...)
		dst = appendJSONString(dst, decString(x))
		return append(dst, '}'), nil
	case VDivision:
		// Division is deliberately inert: an unresolved exact quotient
		// has no canonical digit form, so it never crosses a boundary.
		// Resolve it first (Decimal.rounded / withRemainder).
		return nil, fmt.Errorf("JSON.encode: cannot encode a Decimal.Division — resolve it with Decimal.rounded or Decimal.withRemainder first")
	case VUnit:
		return append(dst, "null"...), nil
	case VDuration:
		// Wire format is `{"__duration": 1500}`, whole milliseconds, the
		// marker-object shape of __angle, so every decoder rebuilds a
		// Duration rather than a plain Int.
		dst = append(dst, `{"__duration":`...)
		dst = strconv.AppendInt(dst, x.Millis, 10)
		return append(dst, '}'), nil
	case VAngle:
		// Wire format is `{"__angle": 450}`, deci-degrees, the same
		// marker-object shape as __char / __time / __dec, so the client
		// and iOS decoders rebuild an Angle instead of a plain Int. An
		// angle in a model has to survive the wire: it goes to App.shared,
		// through time travel, and into a rules engine replayed on both
		// sides.
		dst = append(dst, `{"__angle":`...)
		dst = strconv.AppendInt(dst, x.Deci, 10)
		return append(dst, '}'), nil
	case VTime:
		// Wire format is `{"__time": "ISO 8601"}`, marker keeps the
		// type round-trippable on the client (jsToMar / iOS decoder
		// recognize it and produce a VTime, not a plain VString).
		// The value itself is ISO so the same wire is readable by
		// non-mar consumers inspecting raw JSON.
		// Milliseconds included, always three places. RFC3339 alone drops
		// them: measured, a Time carrying 1700000000123 encoded to
		// "…:20Z" and decoded back to …000, so every service response
		// with a sub-second Time lost it — while the browser's own
		// encoder (toISOString) kept it. The loss was one-directional and
		// silent.
		//
		// Written straight into the buffer: the layout produces only digits
		// and `-T:.Z` (a year past 9999 is just more digits, a year before
		// 0 a leading `-`), none of which JSON escapes.
		dst = append(dst, `{"__time":"`...)
		dst = time.UnixMilli(x.Millis).UTC().AppendFormat(dst, isoMillis)
		return append(dst, `"}`...), nil
	case VList:
		var err error
		dst = append(dst, '[')
		for i, e := range x.Elements {
			if i > 0 {
				dst = append(dst, ',')
			}
			if dst, err = appendValue(dst, e); err != nil {
				return nil, err
			}
		}
		return append(dst, ']'), nil
	case VRecord:
		// Sorted by field name in code point order (byte order of UTF-8, as
		// sort.Strings gives it; codePointCompare in runtime.js and
		// MarJSONCodec.jsonText use the same), not by the order the literal
		// was written. `Order` is where the fields appeared in the source,
		// which is not part of the value: `{ a = 1, b = 2 }` and
		// `{ b = 2, a = 1 }` are equal, and a pure function gives equal
		// output for equal input.
		var stack [16]string
		names := append(stack[:0], x.Order...)
		sort.Strings(names)
		var err error
		dst = append(dst, '{')
		for i, n := range names {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendJSONString(dst, n)
			dst = append(dst, ':')
			if dst, err = appendValue(dst, x.Fields[n]); err != nil {
				return nil, err
			}
		}
		return append(dst, '}'), nil
	case VCtor:
		// Every constructor, Nothing and Just included, round-trips
		// as `{"__ctor":"Name"}` (zero-arg) or
		// `{"__ctor":"Name","__args":[...]}` (with payload). The marker
		// prefix keeps it distinguishable from user records that happen
		// to have a `tag` field. The JS runtime's jsToMar / marToJs and
		// the iOS MarJSONCodec use the same convention.
		//
		// All constructors tag uniformly, even Nothing and Just. A
		// transparent encoding (Nothing → null, Just x → x) would
		// collide with VUnit's null and break generic decoders for
		// payload records. The tag costs a few bytes on the wire but
		// makes the round-trip predictable.
		//
		// `__args` is written before `__ctor` because every object this
		// encoder emits has its keys in sorted order: see the record case.
		// The iOS encoder hands its tree to JSONSerialization, which can only
		// be made deterministic with `.sortedKeys`, so sorted is the one order
		// all three runtimes can agree on. Decoders look keys up, so none of
		// them notices.
		var err error
		dst = append(dst, '{')
		if len(x.Args) > 0 {
			dst = append(dst, `"__args":[`...)
			for i, a := range x.Args {
				if i > 0 {
					dst = append(dst, ',')
				}
				if dst, err = appendValue(dst, a); err != nil {
					return nil, err
				}
			}
			dst = append(dst, `],`...)
		}
		dst = append(dst, `"__ctor":`...)
		dst = appendJSONString(dst, x.Tag)
		return append(dst, '}'), nil
	case VTuple:
		var err error
		dst = append(dst, '[')
		for i, e := range x.Members {
			if i > 0 {
				dst = append(dst, ',')
			}
			if dst, err = appendValue(dst, e); err != nil {
				return nil, err
			}
		}
		return append(dst, ']'), nil
	case VDict:
		// `{"__dict": [[k1,v1], [k2,v2], ...]}`, the marker tells
		// the decoder side to rebuild a VDict rather than treating
		// the object as a plain VRecord. Pairs ride as 2-element
		// arrays so non-string keys (Int, Float) round-trip too;
		// the alternative (a JSON object for String-keyed dicts,
		// array-of-pairs otherwise) would need the encoder to peek
		// at the first key's type, and would split decoders into
		// two paths.
		var err error
		dst = append(dst, `{"__dict":[`...)
		for i, p := range x.Pairs {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = append(dst, '[')
			if dst, err = appendValue(dst, p.Key); err != nil {
				return nil, err
			}
			dst = append(dst, ',')
			if dst, err = appendValue(dst, p.Value); err != nil {
				return nil, err
			}
			dst = append(dst, ']')
		}
		return append(dst, "]}"...), nil
	case VSet:
		// `{"__set":[i1,i2,...]}`, counterpart to the VDict marker.
		// Items are already sorted; the decoder sorts again anyway
		// (setFromWire), so a hand-crafted payload that arrives out of
		// order still decodes to a valid Set.
		var err error
		dst = append(dst, `{"__set":[`...)
		for i, it := range x.Items {
			if i > 0 {
				dst = append(dst, ',')
			}
			if dst, err = appendValue(dst, it); err != nil {
				return nil, err
			}
		}
		return append(dst, "]}"...), nil
	case VEffect, VFn:
		return nil, fmt.Errorf("JSON.encode: %s has no JSON form.\n"+
			"  Encode the data it was built FROM, and build it on the side that uses it", marKindOf(v))
	}
	// A Go type name is not something the reader wrote. These are the values a
	// runtime builds for itself — a Page, a View, an Entity — and `JSON.encode`
	// takes `a`, so the type system lets one through and the encoder meets it
	// here.
	//
	// This comment used to end "moving it earlier would need the API to stop
	// being polymorphic, which is a language decision rather than a fix". That
	// is true of changing the TYPE and only of that: a call site whose argument
	// is definitely one of these is refused at compile time by the shape lint
	// (see noJSONForm in internal/typecheck/shape_lint.go), with the signature
	// untouched. What is left here is the case the lint cannot be sure about,
	// which is why the sentence still has to be worth reading.
	return nil, fmt.Errorf("JSON.encode: %s has no JSON form.\n"+
		"  Encode the data it was built FROM, and build it on the side that uses it", marKindOf(v))
}

// EncodeValueJSON serializes a Mar Value into the JSON wire format the frontend
// runtime decodes (jsToMar): the same encoding Service responses use (see
// service.go). Exposed so the server layer (internal/jsserve) can hand the
// admin panel its Mar.Admin.* introspection results.
func EncodeValueJSON(v Value) (string, error) {
	return encodeValue(v)
}

// wholeNumber reads a deci-degree payload. Deci-degrees are integral by
// construction and no Mar encoder writes a fraction, so a fractional one is
// refused rather than truncated — the same rule the `__char` payload follows.
func wholeNumber(raw any, unit string) (int64, error) {
	switch n := raw.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, nil
		}
		return 0, fmt.Errorf("the value should be a whole number of %s, got %s", unit, n.String())
	case float64:
		if n == math.Trunc(n) {
			return int64(n), nil
		}
		return 0, fmt.Errorf("the value should be a whole number of %s, got %v", unit, n)
	}
	return 0, fmt.Errorf("the value should be a whole number of %s, got %v", unit, raw)
}

// durationFromWire reads the payload of `{"__duration": ms}`: an integer
// literal (no fraction, no exponent) inside the range of Int. The browser and
// the phone read the same literal text by the same rules, with the same
// sentences.
func durationFromWire(raw any) (Value, error) {
	n, ok := raw.(json.Number)
	if !ok {
		return nil, fmt.Errorf("the value should be a whole number of milliseconds")
	}
	lit := n.String()
	if !integerLiteral(lit) {
		return nil, fmt.Errorf("the value should be a whole number of milliseconds, got %s", lit)
	}
	ms, err := n.Int64()
	if err != nil || !inIntRange(ms) {
		return nil, durationOutOfRange("a duration of " + lit + " milliseconds")
	}
	return VDuration{Millis: ms}, nil
}

// integerLiteral reports whether a JSON number's text is a whole number
// written as one: digits and an optional minus sign.
func integerLiteral(lit string) bool {
	digits := strings.TrimPrefix(lit, "-")
	if digits == "" {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isoMillis is the wire form of a Time: RFC 3339 in UTC with exactly three
// decimal places, which is what JavaScript's toISOString produces. Parsing
// still accepts a value without them, so anything already stored or in flight
// reads back unchanged.
const isoMillis = "2006-01-02T15:04:05.000Z07:00"

// decodeJSON parses a JSON string into a generic Value.
// Numbers become VInt (whole and inside 53 bits) or an exact VDecimal.
// Objects become VRecord.
// Arrays become VList. null becomes VUnit. Booleans become VBool.
//
// Note: this is "untyped" decoding. It does not coerce into custom types.
// For typed decoding, use type-directed conversion at the boundary.
// notJSONMessage is what a document that is not JSON answers, on all three
// runtimes. See the comment at the `jsonDecode` builtin for why it cannot be
// the host parser's own sentence.
const notJSONMessage = "the document is not valid JSON"

// errNotJSON is the SYNTAX failure, kept apart from every other failure
// decodeJSON can report. The two used to arrive as one `error`, and giving
// them one message told a caller who sent `{"__char":"abc"}` — valid JSON, and
// not a character — that their document was not JSON at all.
var errNotJSON = errors.New(notJSONMessage)

func decodeJSON(s string) (Value, error) {
	var raw any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, errNotJSON
	}
	return convertJSON(raw)
}

func convertJSON(raw any) (Value, error) {
	switch x := raw.(type) {
	case nil:
		return VUnit{}, nil
	case bool:
		return VBool{V: x}, nil
	case string:
		return VString{V: x}, nil
	case json.Number:
		if i, err := x.Int64(); err == nil && inIntRange(i) {
			return VInt{V: i}, nil
		}
		// A whole number wider than Int is NOT refused here. It is still a
		// number Mar can hold: a Decimal is 34 significant digits, so
		// 9007199254740992 is exact, and refusing it at the boundary meant a
		// field typed Decimal could not receive a value that fits it. Whether
		// it is acceptable is the CALL SITE's question, and the shape check
		// asks it with the type in hand.
		// A JSON number decodes TEXTUALLY into a Decimal: `19.99` arrives as
		// exactly 19.99, and `1e30` as ten to the thirtieth, never as the
		// double nearest to either. Exponent notation is read here rather than
		// refused, because notation is not information: a sender writing
		// 1e30 and a sender writing the thirty-one digits mean one value, and
		// they used to get different answers.
		if d, err := decimalFromWireNumber(x.String()); err == nil {
			return d, nil
		}
		// Nothing else is a number in Mar. There used to be a VFloat
		// fallback here; the type is gone now, because with this boundary
		// closed nothing could construct one. It had NO TYPE either --
		// TFloat does not exist, so a checked program could never hold one
		// -- and existed only to travel from here to a shape check and fail
		// there, saying "should be a number, got a number", both sides
		// printing the same word.
		//
		// Mar removed Float and added exact Decimal (ADR 0004) so
		// arithmetic could not drift. A float64 admitted at the wire is
		// that drift coming back through another door, which is what
		// intrange.go says about Int in the same breath.
		return nil, fmt.Errorf("JSON: %s is not a number Mar can hold exactly: an Int is at most %d and a Decimal at most %d significant digits and %d decimal places",
			x.String(), MaxSafeInt, maxDecimalDigits, maxDecimalPlaces)

	case []any:
		out := make([]Value, len(x))
		for i, e := range x {
			v, err := convertJSON(e)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return VList{Elements: out}, nil
	case map[string]any:
		// Tagged constructor, round-trips from `{"__ctor": "Tag"}` or
		// `{"__ctor": "Tag", "__args": [...]}`. Same convention the
		// encoder side uses (see VCtor in encodeValue), shared with
		// the JS runtime's jsToMar and the iOS MarJSONCodec. Without
		// this branch, an enum value sent across the wire (e.g.
		// Service.call's request body containing `status = Open`)
		// would arrive at the handler as a plain VRecord and any
		// downstream pattern match, or Repo.findBy enum lookup:
		// would explode with "expected a constructor (got VRecord)".
		// Tagged decimal, `{"__dec": "1.50"}`. Counterpart to the
		// VDecimal encoder: rebuilds the exact coefficient + scale from
		// the digit string, keeping binary floating point out of the
		// round-trip entirely.
		if raw, ok := x["__dec"]; ok && len(x) == 1 {
			s, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("the value should be a decimal number, got `%v`", raw)
			}
			d, err := parseDecimalString(s)
			if err != nil {
				return nil, fmt.Errorf("the value should be a decimal number, got `%s`", s)
			}
			return d, nil
		}
		if tag, ok := x["__ctor"].(string); ok {
			var argsV []Value
			if rawArgs, present := x["__args"]; present {
				arr, ok := rawArgs.([]any)
				if !ok {
					return nil, fmt.Errorf("JSON.decode: __args must be an array (got %T)", rawArgs)
				}
				argsV = make([]Value, len(arr))
				for i, a := range arr {
					v, err := convertJSON(a)
					if err != nil {
						return nil, err
					}
					argsV[i] = v
				}
			}
			return VCtor{Tag: tag, Args: argsV}, nil
		}
		// Tagged dict, `{"__dict": [[k,v], [k,v], ...]}`. Counterpart
		// to the VDict encoder; rebuilds the sorted-pair invariant
		// the runtime expects. Pairs come over as arbitrary nested
		// values; we convertJSON each side and build the dictionary
		// with dictFromWire, which orders the pairs by key and keeps
		// the last pair for a repeated key (so e.g. Int keys 10/2/30
		// land as 2/10/30).
		if rawPairs, ok := x["__dict"]; ok && len(x) == 1 {
			arr, ok := rawPairs.([]any)
			if !ok {
				return nil, fmt.Errorf("the value should be a list of key/value pairs")
			}
			// Same as the set below: a dictionary is sorted by key, so keys
			// of mixed types fail the comparison rather than the request.
			//
			// The pairs are gathered and the dictionary built once
			// (dictFromWire), not grown a pair at a time. A pair that cannot
			// be read still yields to an EARLIER pair that could not be
			// ordered, which is the order the two were found in when every
			// pair was inserted as soon as it was read.
			const keysOneType = "the value should be a list of key/value pairs whose keys are one type"
			pairs := make([]VDictPair, 0, len(arr))
			refuse := func(err error) (Value, error) {
				if _, orderErr := dictFromWire(pairs); orderErr != nil {
					return nil, errors.New(keysOneType)
				}
				return nil, err
			}
			for _, p := range arr {
				pair, ok := p.([]any)
				if !ok || len(pair) != 2 {
					return refuse(fmt.Errorf("the value should be a key/value pair of two items"))
				}
				k, err := convertJSON(pair[0])
				if err != nil {
					return refuse(err)
				}
				v, err := convertJSON(pair[1])
				if err != nil {
					return refuse(err)
				}
				pairs = append(pairs, VDictPair{Key: k, Value: v})
			}
			d, err := dictFromWire(pairs)
			if err != nil {
				return nil, errors.New(keysOneType)
			}
			return d, nil
		}
		// Tagged set, `{"__set": [i1, i2, ...]}`. Same idea as __dict:
		// setFromWire sorts and drops duplicates, so the invariants
		// hold even for a hand-crafted out-of-order payload.
		if rawItems, ok := x["__set"]; ok && len(x) == 1 {
			arr, ok := rawItems.([]any)
			if !ok {
				return nil, fmt.Errorf("the value should be a list of items")
			}
			// A set is sorted, so building one compares its items — and a
			// payload mixing types made that comparison fail with
			// `comparison: type mismatch`, which says nothing about the
			// request. The items are what is wrong, and saying so is the
			// whole job of a 422.
			//
			// Built once from all the items (setFromWire); see the dictionary
			// above for why an unreadable item defers to an earlier
			// unorderable one.
			const itemsOneType = "the value should be a list of items of one type"
			items := make([]Value, 0, len(arr))
			for _, e := range arr {
				v, err := convertJSON(e)
				if err != nil {
					if _, orderErr := setFromWire(items); orderErr != nil {
						return nil, errors.New(itemsOneType)
					}
					return nil, err
				}
				items = append(items, v)
			}
			s, err := setFromWire(items)
			if err != nil {
				return nil, errors.New(itemsOneType)
			}
			return s, nil
		}
		// Tagged char, `{"__char": "x"}`. Counterpart to VChar's
		// encoder, and exactly one Unicode scalar or nothing.
		//
		// It used to take the FIRST scalar and drop the rest, on the
		// grounds that the type system enforces single-codepoint
		// semantics at the call site. The input here is EXTERNAL: it did
		// not come from a Mar encoder, which cannot write more than one.
		// So `{"__char":"abc"}` answered 'a' and `{"__char":"日本"}`
		// answered '日', silently — while an EMPTY payload failed loudly,
		// in this runtime only. The browser and the phone answered U+FFFD
		// for it. Three runtimes, three answers to one JSON document.
		if s, ok := x["__char"].(string); ok && len(x) == 1 {
			n := 0
			var first rune
			for _, r := range s {
				if n == 0 {
					first = r
				}
				n++
			}
			if n != 1 {
				return nil, fmt.Errorf("the value should be a single character, got %d", n)
			}
			return VChar{V: first}, nil
		}
		// Tagged angle, `{"__angle": 450}`, deci-degrees. Counterpart to
		// the VAngle encoder. Wrapped on the way in rather than trusted:
		// the payload crossed a network boundary, and every other way to
		// build an Angle normalizes, so this one does too.
		//
		// The type assertion here used to be `.(float64)`, which this
		// decoder can never produce: it runs with `UseNumber()`, so every
		// JSON number arrives as a `json.Number`. The branch was dead, and
		// an encoded Angle came back as the plain RECORD
		// `{ __angle = 4500 }` — while the browser and the phone rebuilt an
		// Angle from the same bytes. A rules engine replayed on both sides
		// is exactly what the encoder's comment says this marker is for.
		if raw, present := x["__angle"]; present && len(x) == 1 {
			d, err := wholeNumber(raw, "deci-degrees")
			if err != nil {
				return nil, err
			}
			return VAngle{Deci: posMod(d, deciFull)}, nil
		}
		// Tagged duration, `{"__duration": 1500}`, whole milliseconds inside
		// the range of Int. Counterpart to the VDuration encoder.
		if raw, present := x["__duration"]; present && len(x) == 1 {
			return durationFromWire(raw)
		}
		// Tagged time, `{"__time": "ISO 8601"}`. Counterpart to the
		// VTime encoder; reconstructing a VTime here keeps round-trip
		// fidelity so `createdAt : Time` stays a Time after a service
		// call (rather than degrading to a VString that no longer
		// pattern-matches against Time-shaped APIs).
		//
		// The message carries no host detail. It used to quote Go's own
		// `time.Parse` error, which no other runtime can produce — and the
		// other two did not fail at all: a payload that would not parse
		// fell through to a plain RECORD there.
		if iso, ok := x["__time"].(string); ok && len(x) == 1 {
			t, err := time.Parse(time.RFC3339, iso)
			if err != nil {
				return nil, fmt.Errorf("the value should be a date and time, got `%s`", iso)
			}
			return VTime{Millis: t.UnixMilli()}, nil
		}
		// A decoded object has no declared field order to inherit: the
		// JSON text's order is gone by the time it is a Go map, and map
		// iteration is deliberately randomized. Sorting is the only order
		// available that is the same twice, and Order is what Display and
		// the missing-field error print, so without it the same value
		// reads differently run to run.
		fields := make(map[string]Value, len(x))
		order := make([]string, 0, len(x))
		for k, v := range x {
			val, err := convertJSON(v)
			if err != nil {
				return nil, err
			}
			fields[k] = val
			order = append(order, k)
		}
		sort.Strings(order)
		return VRecord{Fields: fields, Order: order}, nil
	}
	return nil, fmt.Errorf("JSON.decode: unsupported type %T", raw)
}

// decodeWithShape is JSON.decode at a call site whose result type the checker
// knew. Same decoder, one more question asked afterwards: does the value that
// came out match the type the caller is about to treat it as?
//
// The answer goes where a decode failure already goes — `Err` — so this needs
// no new channel and no new vocabulary. `JSON.decode "42"` at type
// `Result String User` used to be `Ok 42`, and reading `.name` off it died
// inside the runtime with a message about records; now it is
// `Err "the value should be an object, got a whole number"`, which is the
// same sentence a bad request body gets.
func decodeWithShape(shape WireShape) Value {
	return nativeFn(1, func(args []Value) (Value, error) {
		s, ok := args[0].(VString)
		if !ok {
			return nil, fmt.Errorf("JSON.decode: expected String")
		}
		v, err := decodeJSON(s.V)
		if err != nil {
			return VCtor{Tag: "Err", Args: []Value{VString{V: err.Error()}}}, nil
		}
		if err := CheckWire(v, shape); err != nil {
			return VCtor{Tag: "Err", Args: []Value{VString{V: decodeShapeMessage(err)}}}, nil
		}
		return VCtor{Tag: "Ok", Args: []Value{CoerceWire(v, shape)}}, nil
	})
}

// decodeShapeMessage says "the value" where a request says "the request". The
// wording is otherwise identical on purpose: one decoder, one vocabulary, so
// the sentence a developer learns from a 422 is the one they read here.
func decodeShapeMessage(err error) string {
	return strings.Replace(err.Error(), "the request", "the value", 1)
}
