package runtime

import (
	"strings"
	"testing"
)

// A path parameter crosses the boundary twice: `linkTo` writes it into a URL,
// and the router reads it back. The two halves are separate functions, and
// what the user exercises is the round trip — clicking a link the app itself
// built.
//
// Both halves moved this session, in opposite directions: the encoder gained
// one URL encoding (it did none at all, so a slug with a slash became TWO
// segments and stopped matching its own pattern), and the decoder gained the
// 53-bit bound (it admitted the whole of int64, so a URL anyone can type
// meant one number on the server and another in the browser).
//
// This is the test that says they still meet.

func roundTrip(t *testing.T, ty string, v Value) (Value, bool) {
	t.Helper()
	enc, err := encodePathSegment(v, ty)
	if err != nil {
		t.Fatalf("%s: encoding %s: %v", ty, v.Display(), err)
	}
	// The router hands the matcher a percent-DECODED segment, which is what
	// decodePathSegment documents; do the same here rather than pretending
	// the raw form is what arrives.
	decoded := enc
	if ty == "String" {
		decoded = mustUnescape(t, enc)
	}
	return decodePathSegment(decoded, ty)
}

func mustUnescape(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+2 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		var n byte
		for _, c := range s[i+1 : i+3] {
			n <<= 4
			switch {
			case c >= '0' && c <= '9':
				n |= byte(c - '0')
			case c >= 'A' && c <= 'F':
				n |= byte(c-'A') + 10
			default:
				t.Fatalf("not a percent escape: %q", s[i:i+3])
			}
		}
		b.WriteByte(n)
		i += 2
	}
	return b.String()
}

func TestAStringParameterSurvivesTheRoundTrip(t *testing.T) {
	for _, raw := range []string{
		"ok", "a b", "a/b", "a?b", "a#b", "ç", "a&b", "a'b", "a(b)", "a-b._~c",
	} {
		back, ok := roundTrip(t, "String", VString{V: raw})
		if !ok {
			t.Errorf("%q encoded into a URL the router will not match", raw)
			continue
		}
		if s, isStr := back.(VString); !isStr || s.V != raw {
			t.Errorf("%q came back as %v", raw, back)
		}
	}
}

func TestAnIntParameterSurvivesTheRoundTrip(t *testing.T) {
	for _, n := range []int64{0, 1, -1, 12345, MaxSafeInt, MinSafeInt} {
		back, ok := roundTrip(t, "Int", VInt{V: n})
		if !ok {
			t.Errorf("%d encoded into a URL the router will not match", n)
			continue
		}
		if got, isInt := back.(VInt); !isInt || got.V != n {
			t.Errorf("%d came back as %v", n, back)
		}
	}
}

func TestAnEnumParameterSurvivesTheRoundTrip(t *testing.T) {
	// The encoder lowercases the tag and the decoder matches
	// case-insensitively — which is why two constructors differing only in
	// case cannot both be routed, and why `mar check` refuses that.
	RegisterEnumType("Role", []string{"Admin", "Member"}, map[string]int{})
	t.Cleanup(ResetEnumTypes)
	for _, tag := range []string{"Admin", "Member"} {
		back, ok := roundTrip(t, "Role", VCtor{Tag: tag})
		if !ok {
			t.Errorf("`%s` encoded into a URL the router will not match", tag)
			continue
		}
		if c, isCtor := back.(VCtor); !isCtor || c.Tag != tag {
			t.Errorf("`%s` came back as %v", tag, back)
		}
	}
}

func TestTheTwoHalvesHandleTheSameTypes(t *testing.T) {
	// A type one half knows and the other does not is a link the app builds
	// and cannot follow. `String`, `Int` and a registered enum are the whole
	// set on both sides.
	RegisterEnumType("Role", []string{"Admin"}, map[string]int{})
	t.Cleanup(ResetEnumTypes)
	for _, tc := range []struct {
		ty     string
		value  Value
		urlBit string
	}{
		{"String", VString{V: "x"}, "x"},
		{"Int", VInt{V: 1}, "1"},
		{"Role", VCtor{Tag: "Admin"}, "admin"},
	} {
		if _, err := encodePathSegment(tc.value, tc.ty); err != nil {
			t.Errorf("%s: the encoder does not know it: %v", tc.ty, err)
		}
		if _, ok := decodePathSegment(tc.urlBit, tc.ty); !ok {
			t.Errorf("%s: the decoder does not know it", tc.ty)
		}
	}
	// And a type neither knows fails on both sides rather than on one.
	if _, err := encodePathSegment(VString{V: "x"}, "Nonsense"); err == nil {
		t.Error("the encoder accepted a type it cannot write")
	}
	if _, ok := decodePathSegment("x", "Nonsense"); ok {
		t.Error("the decoder accepted a type it cannot read")
	}
}
