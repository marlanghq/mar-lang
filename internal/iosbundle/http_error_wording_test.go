package iosbundle

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parityfixture"
)

// The sentence a user reads when a service call fails, on the phone.
//
// `decodeServerError` says in its own comment that it mirrors the JS runtime's
// function of the same name, and nothing checked it: comparing the phone used
// to cost a simulator, so the promise stayed prose. It was not true. For
// `{"error":""}` the browser said `HTTP 500` and the phone handed the user the
// literal `{"error":""}` — the raw JSON this function exists to keep off
// screens, produced by the one branch that treated an empty code as no
// envelope at all.
//
// The table lives in internal/parityfixture next to the browser's half, so the
// two clients are held to one list rather than to two copies of it.
func TestThePhoneWordsAFailedCallLikeTheBrowser(t *testing.T) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed")
	}
	if testing.Short() {
		t.Skip("compiles the Swift core; not for -short")
	}
	dir := t.TempDir()
	if err := assembleSwiftCore(t, dir); err != nil {
		t.Fatalf("assembling the core: %v", err)
	}
	bin := filepath.Join(dir, "driver")
	build := exec.Command(swiftc, "-o", bin)
	sources, _ := filepath.Glob(filepath.Join(dir, "*.swift"))
	build.Args = append(build.Args, sources...)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("swiftc: %v\n%s", err, out)
	}

	for _, c := range parityfixture.HTTPErrorCases {
		t.Run(c.Why, func(t *testing.T) {
			out, err := exec.Command(bin, "--http-error", itoa(c.Status), c.Body).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			// Trailing newline only: the driver prints with `print`, and one
			// of these answers is two spaces, which TrimSpace would eat. That
			// is not hypothetical — it made a real divergence look like two.
			got := strings.TrimSuffix(string(out), "\n")
			if got != c.Says {
				t.Errorf("%d with body %q\n  want: %q\n  got:  %q", c.Status, c.Body, c.Says, got)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
