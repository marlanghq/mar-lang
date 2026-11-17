package scaffold

import (
	"encoding/json"
	"strings"
	"testing"
)

// The program is embedded in the page as JSON inside a <script> element. The
// element must hold no `<` at all: `</script` would end it early and `<!--`
// changes how the parser reads it, and the program text is the app's own
// strings. The JSON read back is the program that went in.
func TestTheEmbeddedProgramHoldsNoMarkup(t *testing.T) {
	program := []byte(`{"s":"</script><img src=x onerror=alert(1)><!-- & /"}`)
	html := buildIndexHTML("en", "App", program)

	const open = `id="mar-program">`
	i := strings.Index(html, open)
	if i < 0 {
		t.Fatalf("no program element in:\n%s", html)
	}
	rest := html[i+len(open):]
	j := strings.Index(rest, "</script>")
	if j < 0 {
		t.Fatal("the program element is not closed")
	}
	embedded := rest[:j]
	if strings.Contains(embedded, "<") {
		t.Fatalf("the program element holds a `<`: %s", embedded)
	}
	var got, want map[string]string
	if err := json.Unmarshal([]byte(embedded), &got); err != nil {
		t.Fatalf("the embedded program is not JSON: %v", err)
	}
	_ = json.Unmarshal(program, &want)
	if got["s"] != want["s"] {
		t.Fatalf("the program read back is %q, want %q", got["s"], want["s"])
	}
}
