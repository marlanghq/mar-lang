package iosbundle

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parityfixture"
)

// The phone's half of the auth port, against the same table.
//
// Both clients are driven at the layer where the answer is decided:
// makeAuthOutcomeResult here, a whole Auth.verifyCode against a fake fetch in
// the browser's test. The phone's failure was the worse of the two — a body
// that would not parse became `()`, so `Auth.SignedIn ()` was handed to code
// typed as the app's user, and nothing anywhere said so.
func TestThePhoneChecksWhoTheSignInEndpointSaysYouAre(t *testing.T) {
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

	for _, c := range parityfixture.AuthPortCases {
		t.Run(c.Why, func(t *testing.T) {
			out, err := exec.Command(bin, "--auth-verify", "200", c.Body).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			got := strings.TrimSuffix(string(out), "\n")
			if c.Says == "" {
				// The accepted case prints the built value; what matters is
				// that it IS the signed-in ctor and carries the record.
				if !strings.Contains(got, `tag: "SignedIn"`) || !strings.Contains(got, "record") {
					t.Errorf("%q should have signed the user in, and answered %q", c.Body, got)
				}
				return
			}
			if got != c.Says {
				t.Errorf("%q\n  want: %q\n  got:  %q", c.Body, c.Says, got)
			}
		})
	}
}
