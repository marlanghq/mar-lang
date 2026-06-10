package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// jsonBuiltins returns runtime functions for JSON encode/decode of
// any Value to/from a JSON string. The mapping mirrors the codec rules
// in docs/mar.md.
//
//	JSON.encode  : a -> String
//	JSON.decode  : String -> Result String a   -- (decoded shape is "any" — uses VRecord/VList/VBool/etc.)
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
//   - VFloat -> number
//   - VString -> string
//   - VBool -> true/false
//   - VUnit -> null  (only () encodes as bare null; the decoder side
//     reads null as VUnit unambiguously)
//   - VList -> array
//   - VRecord -> object
//   - Every VCtor — Nothing included — uses the {"__ctor":"Name"} marker
//     (with optional "__args"). Tagging Nothing like every other ctor
//     keeps it distinguishable from VUnit on the wire: a service
//     `Int -> ()` returns bare null, and `Ok ()` patterns must still
//     match it without colliding with `Nothing`.
func encodeValue(v Value) (string, error) {
	switch x := v.(type) {
	case VInt:
		return strconv.FormatInt(x.V, 10), nil
	case VFloat:
		return strconv.FormatFloat(x.V, 'g', -1, 64), nil
	case VString:
		b, _ := json.Marshal(x.V)
		return string(b), nil
	case VChar:
		// Wire format `{"__char": "x"}` — a 1-char string under the
		// marker. JSON itself has no Char type; this marker tells the
		// decoder side (Go's convertJSON / JS jsToMar / Swift
		// MarJSONCodec) to rebuild a VChar instead of degrading to a
		// 1-char VString.
		b, _ := json.Marshal(string(x.V))
		return `{"__char":` + string(b) + `}`, nil
	case VBool:
		if x.V {
			return "true", nil
		}
		return "false", nil
	case VUnit:
		return "null", nil
	case VDuration:
		return strconv.FormatInt(x.Seconds, 10), nil
	case VTime:
		// Wire format is `{"__time": "ISO 8601"}` — marker keeps the
		// type round-trippable on the client (jsToMar / iOS decoder
		// recognize it and produce a VTime, not a plain VString).
		// The value itself is ISO so the same wire is readable by
		// non-mar consumers inspecting raw JSON.
		iso := time.UnixMilli(x.Millis).UTC().Format(time.RFC3339)
		b, _ := json.Marshal(iso)
		return `{"__time":` + string(b) + `}`, nil
	case VList:
		var sb strings.Builder
		sb.WriteByte('[')
		for i, e := range x.Elements {
			if i > 0 {
				sb.WriteByte(',')
			}
			s, err := encodeValue(e)
			if err != nil {
				return "", err
			}
			sb.WriteString(s)
		}
		sb.WriteByte(']')
		return sb.String(), nil
	case VRecord:
		var sb strings.Builder
		sb.WriteByte('{')
		for i, n := range x.Order {
			if i > 0 {
				sb.WriteByte(',')
			}
			b, _ := json.Marshal(n)
			sb.Write(b)
			sb.WriteByte(':')
			s, err := encodeValue(x.Fields[n])
			if err != nil {
				return "", err
			}
			sb.WriteString(s)
		}
		sb.WriteByte('}')
		return sb.String(), nil
	case VCtor:
		// Every constructor — Nothing and Just included — round-trips
		// as `{"__ctor":"Name"}` (zero-arg) or
		// `{"__ctor":"Name","__args":[...]}` (with payload). The marker
		// prefix keeps it distinguishable from user records that happen
		// to have a `tag` field. The JS runtime's jsToMar / marToJs and
		// the iOS MarJSONCodec use the same convention.
		//
		// All constructors tag uniformly — even Nothing and Just. A
		// transparent encoding (Nothing → null, Just x → x) would
		// collide with VUnit's null and break generic decoders for
		// payload records. The tag costs a few bytes on the wire but
		// makes the round-trip predictable.
		var sb strings.Builder
		sb.WriteString(`{"__ctor":`)
		b, _ := json.Marshal(x.Tag)
		sb.Write(b)
		if len(x.Args) > 0 {
			sb.WriteString(`,"__args":[`)
			for i, a := range x.Args {
				if i > 0 {
					sb.WriteByte(',')
				}
				s, err := encodeValue(a)
				if err != nil {
					return "", err
				}
				sb.WriteString(s)
			}
			sb.WriteByte(']')
		}
		sb.WriteByte('}')
		return sb.String(), nil
	case VTuple:
		var sb strings.Builder
		sb.WriteByte('[')
		for i, e := range x.Members {
			if i > 0 {
				sb.WriteByte(',')
			}
			s, err := encodeValue(e)
			if err != nil {
				return "", err
			}
			sb.WriteString(s)
		}
		sb.WriteByte(']')
		return sb.String(), nil
	case VDict:
		// `{"__dict": [[k1,v1], [k2,v2], ...]}` — the marker tells
		// the decoder side to rebuild a VDict rather than treating
		// the object as a plain VRecord. Pairs ride as 2-element
		// arrays so non-string keys (Int, Float) round-trip too;
		// the alternative (a JSON object for String-keyed dicts,
		// array-of-pairs otherwise) would need the encoder to peek
		// at the first key's type, and would split decoders into
		// two paths.
		var sb strings.Builder
		sb.WriteString(`{"__dict":[`)
		for i, p := range x.Pairs {
			if i > 0 {
				sb.WriteByte(',')
			}
			ks, err := encodeValue(p.Key)
			if err != nil {
				return "", err
			}
			vs, err := encodeValue(p.Value)
			if err != nil {
				return "", err
			}
			sb.WriteByte('[')
			sb.WriteString(ks)
			sb.WriteByte(',')
			sb.WriteString(vs)
			sb.WriteByte(']')
		}
		sb.WriteString("]}")
		return sb.String(), nil
	case VSet:
		// `{"__set":[i1,i2,...]}` — counterpart to the VDict marker.
		// Items are already sorted; the decoder rebuilds via setInsert
		// to keep the sorted invariant even if a hand-crafted payload
		// arrives out of order.
		var sb strings.Builder
		sb.WriteString(`{"__set":[`)
		for i, it := range x.Items {
			if i > 0 {
				sb.WriteByte(',')
			}
			s, err := encodeValue(it)
			if err != nil {
				return "", err
			}
			sb.WriteString(s)
		}
		sb.WriteString("]}")
		return sb.String(), nil
	case VEffect, VFn:
		return "", fmt.Errorf("JSON.encode: cannot encode %s", x.Display())
	}
	return "", fmt.Errorf("JSON.encode: unsupported %T", v)
}

// EncodeValueJSON serializes a Mar Value into the JSON wire format the frontend
// runtime decodes (jsToMar) — the same encoding Service responses use (see
// service.go). Exposed so the server layer (internal/jsserve) can hand the
// admin panel its Mar.Admin.* introspection results.
func EncodeValueJSON(v Value) (string, error) {
	return encodeValue(v)
}

// decodeJSON parses a JSON string into a generic Value.
// Numbers become VInt (if integer) or VFloat. Objects become VRecord.
// Arrays become VList. null becomes VUnit. Booleans become VBool.
//
// Note: this is "untyped" decoding. It does not coerce into custom types.
// For typed decoding, use type-directed conversion at the boundary.
func decodeJSON(s string) (Value, error) {
	var raw any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, err
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
		if i, err := x.Int64(); err == nil {
			return VInt{V: i}, nil
		}
		f, err := x.Float64()
		if err != nil {
			return nil, err
		}
		return VFloat{V: f}, nil
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
		// Tagged constructor — round-trips from `{"__ctor": "Tag"}` or
		// `{"__ctor": "Tag", "__args": [...]}`. Same convention the
		// encoder side uses (see VCtor in encodeValue), shared with
		// the JS runtime's jsToMar and the iOS MarJSONCodec. Without
		// this branch, an enum value sent across the wire (e.g.
		// Service.call's request body containing `status = Open`)
		// would arrive at the handler as a plain VRecord and any
		// downstream pattern match — or Repo.findBy enum lookup —
		// would explode with "expected a constructor (got VRecord)".
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
		// Tagged dict — `{"__dict": [[k,v], [k,v], ...]}`. Counterpart
		// to the VDict encoder; rebuilds the sorted-pair invariant
		// the runtime expects. Pairs come over as arbitrary nested
		// values; we convertJSON each side and then insert into a
		// fresh VDict to preserve sort order via the runtime's own
		// insert routine (so e.g. Int keys 10/2/30 land as 2/10/30).
		if rawPairs, ok := x["__dict"]; ok && len(x) == 1 {
			arr, ok := rawPairs.([]any)
			if !ok {
				return nil, fmt.Errorf("JSON.decode: __dict must be an array of pairs")
			}
			d := VDict{}
			for _, p := range arr {
				pair, ok := p.([]any)
				if !ok || len(pair) != 2 {
					return nil, fmt.Errorf("JSON.decode: __dict pair must be a 2-element array")
				}
				k, err := convertJSON(pair[0])
				if err != nil {
					return nil, err
				}
				v, err := convertJSON(pair[1])
				if err != nil {
					return nil, err
				}
				next, err := dictInsert(d, k, v)
				if err != nil {
					return nil, err
				}
				d = next.(VDict)
			}
			return d, nil
		}
		// Tagged set — `{"__set": [i1, i2, ...]}`. Same idea as __dict:
		// rebuild via setInsert so the sorted/dedup invariants hold
		// even for a hand-crafted out-of-order payload.
		if rawItems, ok := x["__set"]; ok && len(x) == 1 {
			arr, ok := rawItems.([]any)
			if !ok {
				return nil, fmt.Errorf("JSON.decode: __set must be an array")
			}
			s := VSet{}
			for _, e := range arr {
				v, err := convertJSON(e)
				if err != nil {
					return nil, err
				}
				next, err := setInsert(s, v)
				if err != nil {
					return nil, err
				}
				s = next.(VSet)
			}
			return s, nil
		}
		// Tagged char — `{"__char": "x"}`. Counterpart to VChar's
		// encoder. Takes the FIRST Unicode scalar of the string
		// (covers ASCII, BMP, and supplementary planes). A multi-char
		// payload would mean the producer is buggy; we tolerate it
		// silently (first scalar wins) rather than failing loudly,
		// since the type system already enforces single-codepoint
		// semantics at the call site.
		if s, ok := x["__char"].(string); ok && len(x) == 1 {
			for _, r := range s {
				return VChar{V: r}, nil
			}
			return nil, fmt.Errorf("JSON.decode: empty __char")
		}
		// Tagged time — `{"__time": "ISO 8601"}`. Counterpart to the
		// VTime encoder; reconstructing a VTime here keeps round-trip
		// fidelity so `createdAt : Time` stays a Time after a service
		// call (rather than degrading to a VString that no longer
		// pattern-matches against Time-shaped APIs).
		if iso, ok := x["__time"].(string); ok && len(x) == 1 {
			t, err := time.Parse(time.RFC3339, iso)
			if err != nil {
				return nil, fmt.Errorf("JSON.decode: invalid __time %q: %v", iso, err)
			}
			return VTime{Millis: t.UnixMilli()}, nil
		}
		fields := make(map[string]Value, len(x))
		var order []string
		for k, v := range x {
			val, err := convertJSON(v)
			if err != nil {
				return nil, err
			}
			fields[k] = val
			order = append(order, k)
		}
		return VRecord{Fields: fields, Order: order}, nil
	}
	return nil, fmt.Errorf("JSON.decode: unsupported type %T", raw)
}
