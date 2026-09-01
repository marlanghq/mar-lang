package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dev session secret self-heals: a `.mar/dev-secrets.json` that will not
// parse is replaced and dev keeps working. The recovery was right and silent,
// and the silence is the part that costs — every session signed with the old
// secret is dead, so the developer's next click is a sign-in screen with no
// explanation anywhere.
//
// What the caller needs from this function is the difference between a first
// run (nothing to lose, say nothing) and a replacement (say so). Both write
// the same file, so the source string is the only thing that can carry it.
func TestSessionSecretSourceTellsFirstRunFromReplacement(t *testing.T) {
	dir := t.TempDir()
	devFile := filepath.Join(dir, ".mar", "dev-secrets.json")

	first, src, err := ResolveSessionSecret(nil, dir)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first == "" {
		t.Fatal("first run produced no secret")
	}
	if strings.Contains(src, "replaced") {
		t.Fatalf("a first run has no session to invalidate, so it must not report a replacement: %q", src)
	}

	again, src, err := ResolveSessionSecret(nil, dir)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if again != first {
		t.Fatal("a readable file must keep the same secret, or every restart signs everyone out")
	}
	if strings.Contains(src, "newly generated") || strings.Contains(src, "replaced") {
		t.Fatalf("nothing was generated on this run: %q", src)
	}

	if err := os.WriteFile(devFile, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, src, err := ResolveSessionSecret(nil, dir)
	if err != nil {
		t.Fatalf("a file that will not parse must self-heal, not stop dev: %v", err)
	}
	if third == first {
		t.Fatal("the unreadable file should have been replaced")
	}
	if !strings.Contains(src, "replaced") {
		t.Fatalf("this is the case that logs everyone out, and the source string is what says so: %q", src)
	}

	// And the replacement has to leave a file the NEXT run can read, or the
	// hint fires forever and the secret rotates on every restart.
	fourth, src, err := ResolveSessionSecret(nil, dir)
	if err != nil {
		t.Fatalf("fourth run: %v", err)
	}
	if fourth != third || strings.Contains(src, "replaced") {
		t.Fatalf("the rewritten file must be readable: got %q, src %q", fourth, src)
	}
}
