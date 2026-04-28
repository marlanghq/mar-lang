package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// jsonBuiltins returns runtime functions for JSON encode/decode.
//
// MVP: encode/decode any Value to/from a JSON string. The mapping mirrors
// the codec rules in docs/mar.md.
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
//   - VUnit -> null
//   - VList -> array
//   - VRecord -> object
//   - VCtor with Tag=="Just" + 1 arg -> the arg (transparent)
//   - VCtor with Tag=="Nothing" + 0 args -> null
//   - VCtor with Tag=="Ok" + 1 arg -> { "tag": "ok", "value": ... }
//   - VCtor with Tag=="Err" + 1 arg -> { "tag": "err", "value": ... }
//   - Other VCtor with 0 args -> "lowercaseTag" (string)
//   - Other VCtor with N args -> { "tag": "lowercaseTag", "args": [...] }
func encodeValue(v Value) (string, error) {
	switch x := v.(type) {
	case VInt:
		return strconv.FormatInt(x.V, 10), nil
	case VFloat:
		return strconv.FormatFloat(x.V, 'g', -1, 64), nil
	case VString:
		b, _ := json.Marshal(x.V)
		return string(b), nil
	case VBool:
		if x.V {
			return "true", nil
		}
		return "false", nil
	case VUnit:
		return "null", nil
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
		// Maybe — transparent
		if x.Tag == "Just" && len(x.Args) == 1 {
			return encodeValue(x.Args[0])
		}
		if x.Tag == "Nothing" {
			return "null", nil
		}
		// Tag-only constructor → string
		if len(x.Args) == 0 {
			b, _ := json.Marshal(strings.ToLower(x.Tag[:1]) + x.Tag[1:])
			return string(b), nil
		}
		// Constructor with payload → tagged object
		var sb strings.Builder
		sb.WriteString(`{"tag":`)
		b, _ := json.Marshal(strings.ToLower(x.Tag[:1]) + x.Tag[1:])
		sb.Write(b)
		sb.WriteString(`,"args":[`)
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
		sb.WriteString("]}")
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
	case VEffect, VFn:
		return "", fmt.Errorf("JSON.encode: cannot encode %s", x.Display())
	}
	return "", fmt.Errorf("JSON.encode: unsupported %T", v)
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
