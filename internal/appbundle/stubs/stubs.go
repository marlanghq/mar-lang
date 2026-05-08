// Package stubs holds the cross-compiled mar-runtime binaries that
// `mar build` concatenates with an app payload to produce a self-
// contained executable.
//
// The binaries directory is populated by `make stubs`, which cross-
// compiles cmd/mar-runtime for every target listed in TargetList. The
// resulting files are embedded into the main `mar` binary via go:embed,
// so a developer on macOS can `mar build --target linux-amd64` and the
// produced executable runs on a Linux server with zero local toolchain
// requirements.
//
// At a fresh checkout the binaries directory is empty (only a .keep
// placeholder) — `go build ./cmd/mar` succeeds but `mar build` will
// report missing stubs until `make stubs` runs.
package stubs

import (
	"embed"
	"fmt"
	goruntime "runtime"
	"sort"
	"strings"
)

// TargetList is the set of OS/arch pairs we cross-compile a stub for.
// Adding a target here is only useful in combination with a Makefile
// rule that actually produces the binary.
var TargetList = []string{
	"darwin-amd64",
	"darwin-arm64",
	"linux-amd64",
	"linux-arm64",
	"windows-amd64",
}

//go:embed binaries
var stubFS embed.FS

// HostTarget returns the target string matching the running mar binary
// (default for `mar build` when --target isn't specified).
func HostTarget() string {
	return goruntime.GOOS + "-" + goruntime.GOARCH
}

// Get returns the embedded mar-runtime stub bytes for the given target
// (e.g. "linux-amd64", "darwin-arm64"). Returns an error when no stub
// was bundled — typically because `make stubs` didn't run before `mar`
// was compiled.
func Get(target string) ([]byte, error) {
	if !validTarget(target) {
		return nil, fmt.Errorf("appbundle/stubs: unknown target %q (known: %s)", target, strings.Join(TargetList, ", "))
	}
	data, err := stubFS.ReadFile("binaries/" + stubFileName(target))
	if err != nil {
		avail := Available()
		if len(avail) == 0 {
			return nil, fmt.Errorf("appbundle/stubs: no stubs embedded — run `make stubs` and rebuild mar")
		}
		return nil, fmt.Errorf("appbundle/stubs: stub for %q not embedded (have: %s)", target, strings.Join(avail, ", "))
	}
	return data, nil
}

// Available lists the targets for which a stub is actually present in
// the embedded FS. Useful for surfacing helpful errors and for the
// `mar build --target` help text.
func Available() []string {
	entries, err := stubFS.ReadDir("binaries")
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, targetFromFile(name))
	}
	sort.Strings(out)
	return out
}

// stubFileName returns the on-disk filename for a target. Windows
// binaries get .exe so they keep working when extracted as-is.
func stubFileName(target string) string {
	if strings.HasPrefix(target, "windows-") {
		return target + ".exe"
	}
	return target
}

func targetFromFile(filename string) string {
	return strings.TrimSuffix(filename, ".exe")
}

func validTarget(target string) bool {
	for _, t := range TargetList {
		if t == target {
			return true
		}
	}
	return false
}
