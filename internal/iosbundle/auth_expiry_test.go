package iosbundle

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parityfixture"
)

// The phone's half of what a 401 decides. See parityfixture.AuthExpiryCases
// for why this is a function now rather than a method on an @Observable.
func TestThePhoneDecidesAnExpiredSessionLikeTheBrowser(t *testing.T) {
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

	for _, c := range parityfixture.AuthExpiryCases {
		t.Run(c.Why, func(t *testing.T) {
			flag := "false"
			if c.AlreadyRedirecting {
				flag = "true"
			}
			out, err := exec.Command(bin, "--auth-expiry", c.SignInPath, flag, c.CurrentPath).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			got := strings.TrimSuffix(string(out), "\n")
			want := boolWord(c.Handled) + "|" + boolWord(c.Navigate) + "|" + c.Capture
			if got != want {
				t.Errorf("signInPath=%q redirecting=%v currentPath=%q\n  want: %q\n  got:  %q",
					c.SignInPath, c.AlreadyRedirecting, c.CurrentPath, want, got)
			}
		})
	}
}

func boolWord(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
