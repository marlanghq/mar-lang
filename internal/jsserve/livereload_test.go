package jsserve

import (
	"strings"
	"testing"
)

// The dev server pipes compile errors through `jsonError` and broadcasts
// them via SSE to every connected browser. The source string comes from
// `diag.Format(err)`, which adds ANSI color escapes when stderr is a
// TTY — and stderr is ALWAYS a TTY when the user runs `mar dev`
// interactively. The browser overlay just renders text, so without
// stripping these codes the user sees garbage like
// `[1;31mType error:[0m argument has the wrong type: ...` instead of
// the real error message.
func TestJsonErrorStripsANSI(t *testing.T) {
	colored := "\x1b[1;31mType error:\x1b[0m argument has the wrong type: " +
		"expected \x1b[38;5;245mList Attr\x1b[0m, got List (Attr Msg)"
	got := jsonError(colored)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("payload still contains ESC: %q", got)
	}
	for _, bad := range []string{"[1;31m", "[0m", "[38;5;245m"} {
		if strings.Contains(got, bad) {
			t.Fatalf("payload still contains %q: %s", bad, got)
		}
	}
	// And the actual message text survived.
	for _, want := range []string{"Type error:", "argument has the wrong type:", "List Attr", "List (Attr Msg)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("payload lost %q: %s", want, got)
		}
	}
}

func TestStripANSIVariants(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"\x1b[1;31mhello\x1b[0m", "hello"},
		{"\x1b[38;5;245mdim\x1b[0m text", "dim text"},
		{"no escapes here", "no escapes here"},
		{"", ""},
		{"\x1b[Kclear line", "clear line"},
		// Multiple codes back-to-back.
		{"\x1b[1m\x1b[31mboldred\x1b[0m", "boldred"},
	}
	for _, tc := range cases {
		if got := stripANSI(tc.in); got != tc.want {
			t.Errorf("stripANSI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
