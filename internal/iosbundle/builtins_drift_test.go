package iosbundle

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"mar/internal/typecheck"
)

// TestIOSBuiltinsCoverClientStdlib catches the class of bug where a
// builtin gets added to typecheck (so user code compiles) and to the
// Go runtime (so server-side eval works), but never lands in the iOS
// Swift runtime — leading to "view failed: unbound name: X" at runtime
// on device.
//
// Source of truth: every qualified name registered in
// typecheck.BaseEnv() that is reachable from frontend code (filtered
// via typecheck.IsBackendOnlyBuiltin). The iOS Swift bundle MUST
// define each via `env.define("Foo.bar", ...)`.
//
// When this test fails: implement each missing builtin in
// `internal/iosbundle/template/Sources/MarBuiltins.swift`, mirroring
// the Go runtime's semantics. If a name is genuinely server-only,
// add it to typecheck.IsBackendOnlyBuiltin (and the parallel JS test
// will pick the same answer up).
func TestIOSBuiltinsCoverClientStdlib(t *testing.T) {
	required := typecheck.BaseQualifiedSymbols()

	defined, err := readSwiftBuiltinNames()
	if err != nil {
		t.Fatalf("reading Swift builtins: %v", err)
	}

	// The ~100 Keyboard.Key constructors aren't qualified aliases, so they never
	// enter this required set at all; the input mirrors (Keyboard.watch /
	// Gamepad.watch / Canvas.watchPointers / Canvas.watchSize) ARE qualified
	// aliases and are defined natively in the Swift bundle, so they're required
	// and present — nothing to exempt.
	// The web-first subsystems (Canvas, Sound, Gamepad, Keyboard, Device) used
	// to be deferred here — implemented in the JS runtime, absent on iOS. They
	// are now all native: MarCanvas (SwiftUI Canvas draw-list), MarSound
	// (AVAudioEngine chip synth), MarInput (GameController Gamepad + GCKeyboard
	// + trait/scene Device), wired through MarPageRuntime's generalized sub
	// reconciler. So nothing WAS deferred, and the map is here so re-deferring a
	// web-first builtin stays a one-line change.
	//
	// Shared (ADR-0026) is the current occupant, landed web-first the way Canvas
	// and Sound did. What iOS is missing is not the three builtins in isolation
	// but the store behind them: a model keyed by def identity, PageRuntime
	// resolving its four functions live instead of capturing them at init, and
	// the sub reconciler carrying a destination per tagger. Stubbing the
	// builtins to satisfy this test would be worse than the gap — an app would
	// compile for iOS and then quietly lose its cart.
	iosDeferred := map[string]bool{
		"App.shared":      true,
		"Page.withShared": true,
		"Cmd.toShared":    true,
	}

	var missing []string
	for name := range required {
		if typecheck.IsBackendOnlyBuiltin(name) || iosDeferred[name] {
			continue
		}
		if !defined[name] {
			missing = append(missing, name)
		}
	}
	t.Logf("iOS canvas is deferred (web-first): %d Canvas builtins exempt from the Swift coverage check", len(iosDeferred))
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("MarBuiltins.swift is missing %d builtin(s) reachable from frontend code:\n  %s\n\nFix: implement each in internal/iosbundle/template/Sources/MarBuiltins.swift, mirroring the Go runtime's semantics.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestIOSCoversQualifiedUnionCtors closes a hole the coverage test above
// cannot see. That test's source of truth, BaseQualifiedSymbols(), reports
// only builtins carrying BOTH a bare and a dotted name (the qualifiedAliases
// mapping). Union constructors are dotted-ONLY — `Canvas.Alpha` is a direct
// key in baseBindings() — so they never enter the required set, and deleting
// one from the Swift bundle passes every other test in the tree (verified by
// deleting Canvas.Multiply, 2026-07-15; 146 names were invisible this way).
//
// The requirement here is derived from the union tables themselves (every
// CustomType with a Module), which are also what internal/ctorgen generates
// MarBuiltinCtors.swift from — so this is the belt to that generator's
// braces: ctorgen's staleness test proves the generated file matches the
// tables, and this test proves the names actually sit in the Swift bundle
// (catching, say, a deleted generated file that something still compiles
// without).
func TestIOSCoversQualifiedUnionCtors(t *testing.T) {
	defined, err := readSwiftBuiltinNames()
	if err != nil {
		t.Fatalf("reading Swift builtins: %v", err)
	}

	var missing []string
	checked := 0
	for union, ct := range typecheck.BaseCustomTypes() {
		if ct.Module == "" {
			continue
		}
		if len(ct.CtorOrder) == 0 {
			t.Fatalf("builtin union %q has Module %q but no CtorOrder; nothing would be checked", union, ct.Module)
		}
		for _, ctor := range ct.CtorOrder {
			checked++
			if name := ct.Module + "." + ctor; !defined[name] {
				missing = append(missing, name)
			}
		}
	}
	if checked < 130 {
		t.Fatalf("only %d qualified union ctors checked — the Module fields on the builtin unions look stale", checked)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("Swift bundle is missing %d qualified union constructor(s):\n  %s\n\nFix: go generate ./internal/ctorgen (they are emitted into MarBuiltinCtors.swift). Swift has no bare-tag fallback — an unregistered name throws at runtime on device.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// readSwiftBuiltinNames extracts every `env.define("...")` first-arg
// string literal from the Sources/*.swift files. Only the FIRST
// argument matters — that's the registry key; the second is the value
// bound to it.
//
// Scans every Sources/*.swift file (MarBuiltins.swift, MarDict.swift,
// MarChar.swift, …) so the drift detector covers builtins regardless
// of which module they're registered in.
func readSwiftBuiltinNames() (map[string]bool, error) {
	dir := filepath.Join("template", "Sources")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	// `env.define("name", ...)` binds one key. `env.defineFn("bare",
	// "dotted", ...)` — the two-key convenience the web-first subsystems
	// (Sound / Canvas / Gamepad / Keyboard / Device) use — binds BOTH the bare
	// desugared name and the dotted user-facing name, so capture both string
	// literals there. The dotted (2nd) arg is the qualified symbol the drift
	// coverage check looks for.
	re := regexp.MustCompile(`env\.define\(\s*"([^"]+)"`)
	reFn := regexp.MustCompile(`env\.defineFn\(\s*"([^"]+)"\s*,\s*"([^"]+)"`)
	out := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".swift") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, m := range re.FindAllSubmatch(data, -1) {
			out[string(m[1])] = true
		}
		for _, m := range reFn.FindAllSubmatch(data, -1) {
			out[string(m[1])] = true
			out[string(m[2])] = true
		}
	}
	return out, nil
}

// TestSwiftDeclarationStubsAreComplete — the iOS half of the same hole
// the web test describes. `Entity.*` is backend-only for CALLING, but a
// module that declares an entity still has to EVALUATE on the client,
// so every schema helper needs a stub here too. Both client runtimes
// were missing exactly `Entity.enum` until 2026-07-20.
func TestSwiftDeclarationStubsAreComplete(t *testing.T) {
	defined, err := readSwiftBuiltinNames()
	if err != nil {
		t.Fatalf("reading Swift builtins: %v", err)
	}

	var missing []string
	for name := range typecheck.BaseQualifiedSymbols() {
		if !strings.HasPrefix(name, "Entity.") {
			continue
		}
		if !defined[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("MarBuiltins.swift is missing %d Entity stub(s) that a shared module would need to evaluate:\n  %s\n\nFix: add an `env.define(\"Entity.x\", ...)` stub next to the others. It does no work — it only has to exist.",
			len(missing), strings.Join(missing, "\n  "))
	}
}
