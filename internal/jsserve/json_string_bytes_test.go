package jsserve

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/runtime"
)

// JSON.encode writes a string as JavaScript's JSON.stringify writes it, on the
// server as in the browser, for every Unicode scalar. The server's encoder is
// written out by hand (runtime.appendJSONString), so this compares it with the
// real JSON.stringify of node over all 1,112,064 scalars, in chunks of 4096,
// one JSON string per line on each side.
func TestTheServerWritesEveryStringAsJSONStringifyDoes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	const chunk = 4096
	var scalars []rune
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		scalars = append(scalars, r)
	}

	var goOut bytes.Buffer
	for i := 0; i < len(scalars); i += chunk {
		end := min(i+chunk, len(scalars))
		s, err := runtime.EncodeValueJSON(runtime.VString{V: string(scalars[i:end])})
		if err != nil {
			t.Fatal(err)
		}
		goOut.WriteString(s)
		goOut.WriteByte('\n')
	}

	script := `
const chunk = ` + "4096" + `;
const scalars = [];
for (let r = 0; r <= 0x10FFFF; r++) {
  if (r >= 0xD800 && r <= 0xDFFF) continue;
  scalars.push(String.fromCodePoint(r));
}
const lines = [];
for (let i = 0; i < scalars.length; i += chunk) {
  lines.push(JSON.stringify(scalars.slice(i, i + chunk).join('')));
}
process.stdout.write(lines.join('\n') + '\n');
`
	path := filepath.Join(t.TempDir(), "stringify.js")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	jsOut, err := exec.Command(node, path).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	if bytes.Equal(goOut.Bytes(), jsOut) {
		return
	}
	goLines := strings.Split(goOut.String(), "\n")
	jsLines := strings.Split(string(jsOut), "\n")
	for i := range goLines {
		if i >= len(jsLines) || goLines[i] == jsLines[i] {
			continue
		}
		start := i * chunk
		for k := start; k < min(start+chunk, len(scalars)); k++ {
			one, _ := runtime.EncodeValueJSON(runtime.VString{V: string(scalars[k])})
			if !strings.Contains(jsLines[i], strings.Trim(one, `"`)) {
				t.Fatalf("U+%04X: the server writes %s; JSON.stringify differs in chunk %d", scalars[k], one, i)
			}
		}
		t.Fatalf("chunk %d differs:\n  server %.200q\n  node   %.200q", i, goLines[i], jsLines[i])
	}
	t.Fatalf("the outputs differ in length: %d and %d lines", len(goLines), len(jsLines))
}
