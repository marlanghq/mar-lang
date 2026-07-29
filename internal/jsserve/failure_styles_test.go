package jsserve

import (
	"os"
	"strings"
	"testing"
)

// The failure screens are the only ones that cannot assume a view was drawn.
//
// `ensureUIStyles` is called from the renderers, one per view tag, so the
// stylesheet arrives as a side effect of something rendering. That holds for
// every screen except these two: a cold load whose `init` or `view` throws
// never reaches a renderer, and the message drew as unstyled black text on
// white — found by taking a screenshot, not by a test, because the tests
// checked the class and the words and nothing checks that CSS arrived.
//
// This is a source-level assertion rather than a rendered one. It cannot prove
// the page LOOKS right; it proves the one call that was missing is present,
// which is the regression that actually happened. Proving the rendering would
// need a DOM harness the repo does not have.
func TestFailureScreensInstallTheStylesheetThemselves(t *testing.T) {
	src, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatalf("read runtime.js: %v", err)
	}
	for _, fn := range []string{"reportRuntimeError", "presentPageFailure"} {
		body, ok := functionBody(string(src), "function "+fn+"(")
		if !ok {
			t.Errorf("%s is gone — if it was renamed, this test has to follow it", fn)
			continue
		}
		if !strings.Contains(body, "ensureUIStyles()") {
			t.Errorf("%s must call ensureUIStyles(): it can be reached before any "+
				"renderer has installed the stylesheet, and then the message "+
				"draws unstyled", fn)
		}
	}
}

// functionBody returns the text of the function whose declaration starts with
// `header`, by counting braces from the first one after it.
func functionBody(src, header string) (string, bool) {
	i := strings.Index(src, header)
	if i < 0 {
		return "", false
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		return "", false
	}
	start := i + open
	depth := 0
	for j := start; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : j+1], true
			}
		}
	}
	return "", false
}
