package project

import "testing"

// A malformed mar.json is met by a message the reader can act on, not by Go's
// parser vocabulary. `humanizeJSONError` does the rephrasing, and it keeps the
// offending character the stdlib embedded — dropping it would turn "unexpected
// character `}`" into "unexpected character", which is true and useless.
//
// The extraction had no test, and it is exactly the kind of string surgery
// that keeps working until the day it silently returns "".
func TestAMalformedManifestReadsLikeEnglish(t *testing.T) {
	cases := []struct{ name, stdlib, want string }{
		{
			"a trailing comma before a key",
			`invalid character '}' looking for beginning of object key string`,
			"unexpected character '}' where a key (a quoted string) was expected",
		},
		{
			"a missing comma between pairs",
			`invalid character '"' after object key:value pair`,
			`unexpected character '"' after a key/value pair`,
		},
		{
			"a value that is not one",
			`invalid character 'x' looking for beginning of value`,
			"unexpected character 'x' where a value was expected",
		},
		{
			"a file that stops mid-structure",
			"unexpected end of JSON input",
			"the file ends mid-structure (a quote, brace, or bracket is unclosed)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanizeJSONError(tc.stdlib); got != tc.want {
				t.Errorf("\n  got:  %s\n  want: %s", got, tc.want)
			}
		})
	}

	// A message this does not recognise is passed through rather than
	// swallowed: showing something verbatim beats dropping information.
	t.Run("an unfamiliar message survives", func(t *testing.T) {
		const odd = "something the stdlib has not said before"
		if got := humanizeJSONError(odd); got != odd {
			t.Errorf("got %q", got)
		}
	})

	// The character extraction itself, including the shapes that must NOT
	// produce a half-parsed token.
	t.Run("the character extraction", func(t *testing.T) {
		for _, tc := range []struct{ in, want string }{
			{`invalid character '}' after x`, `'}'`},
			{`invalid character '"' after x`, `'"'`},
			{`invalid character '`, ""},         // truncated
			{`invalid character x after y`, ""}, // no quotes
			{`some other message`, ""},          // wrong idiom
		} {
			if got := extractInvalidChar(tc.in); got != tc.want {
				t.Errorf("extractInvalidChar(%q) = %q, want %q", tc.in, got, tc.want)
			}
		}
	})
}
