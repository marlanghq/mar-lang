package runtime

import "testing"

// A Go string can hold bytes that are not UTF-8 (a path parameter arrives
// percent-decoded and unchecked). Each such byte is written as U+FFFD, so the
// JSON is valid and the same whatever encoding/json would have done.
func TestAByteThatIsNotUTF8IsWrittenAsTheReplacementCharacter(t *testing.T) {
	cases := map[string]string{
		"a\xffb":         "\"a�b\"",
		"x\xed\xa0\x80y": "\"x���y\"",
		"\xf0\x9f\x98":   "\"���\"",
		"ok é 😀":         "\"ok é 😀\"",
	}
	for in, want := range cases {
		got, err := EncodeValueJSON(VString{V: in})
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
