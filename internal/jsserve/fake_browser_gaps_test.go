package jsserve

import (
	"errors"
	"strings"
	"testing"

	"mar/internal/parity"
)

// The fake browser reports what it does not have.
//
// Four times in one afternoon a missing member made it LIE rather than fail —
// `closest` answering null, `nextSibling` absent behind a `|| null`,
// `insertBefore` cloning instead of moving, events not bubbling. Each hid a
// whole listener from every test, and each was found by hand, hours apart.
//
// Every element now comes back wrapped, so a DOM member the runtime reads and
// the fake does not implement is recorded. This drives a program through the
// paths a page actually takes — render, navigate, click, type — and asks what
// piled up.
//
// A name appearing here is not automatically a bug: some are members whose
// absence the runtime handles on purpose. It is a list to LOOK at, which is
// exactly what did not exist while four of them sat there.
func TestTheFakeBrowserSaysWhatItIsMissing(t *testing.T) {
	program, err := parity.Compile(listReorderSrc, SerializeModule)
	if err != nil {
		t.Fatalf("compiling the fixture: %v", err)
	}

	out, err := parity.RunWeb(runtimeJS, program, `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
history.replaceState({ marDepth: 0, prevTitle: '' }, '', '/');
window.marRun(program);

// Exercise the paths a page takes: a render happened above; now a keyboard
// interaction through a delegated listener, and a re-render after it.
const handles = [];
(function walk(n) {
  if (n.classList && n.classList.contains('mar-drag-handle')) handles.push(n);
  for (const c of (n.children || [])) walk(c);
})(marRoot);
if (handles[0]) {
  for (const k of [' ', 'ArrowDown', ' ']) {
    handles[0].dispatch('keydown', { key: k, target: handles[0], preventDefault(){}, stopPropagation(){} });
  }
}
const gaps = Array.from(globalThis.__marFakeGaps || []).sort();
process.stdout.write(JSON.stringify(gaps));
`)
	if errors.Is(err, parity.ErrNoNode) {
		t.Skip("node not installed")
	}
	if err != nil {
		t.Fatalf("node run: %v", err)
	}

	// Members the runtime reads and the fake deliberately does not have. Each
	// one is a decision: the runtime copes with the absence, and adding it
	// would be faking behaviour nothing under test depends on.
	// Members the runtime reads and the fake deliberately does not have. Empty
	// today: the one this test found on its first run — `lang` on
	// <html>, which App.locale reads and every web test was therefore
	// running without — was worth implementing rather than excusing.
	known := map[string]string{}

	var unknown []string
	for _, name := range parseJSONStrings(out) {
		if _, ok := known[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		t.Errorf("the runtime read %d DOM member(s) this fake does not have:\n  %s\n\n"+
			"Each is a place the fake answers `undefined` and the runtime carries on — which is "+
			"how four listeners stayed untested. Implement it, or record here why its absence is fine.",
			len(unknown), strings.Join(unknown, "\n  "))
	}
}

func parseJSONStrings(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.Trim(strings.TrimSpace(p), `"`))
	}
	return out
}
