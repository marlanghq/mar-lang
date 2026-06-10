package jsserve

import (
	"strings"
	"testing"
)

// The SwiftUI-style layout model lives in runtime.js as CSS: hstack
// children HUG their content; `spacer` and `expand` are the explicit
// distribution tools. These assertions pin the flip so a later edit
// can't silently revert hstack to the old "stretch every child"
// (flex: 1) behavior — the source of the "alignment is implicit /
// secret knowledge" problem this model was built to fix.
func TestHStackHugsByDefault(t *testing.T) {
	css := runtimeJS

	// The hug rule: hstack children take their natural width and do NOT
	// stretch (flex: 0 1 auto = grow 0, shrink 1, basis auto).
	if !strings.Contains(css, ".mar-hstack > * { flex: 0 1 auto") {
		t.Error("hstack children must hug (flex: 0 1 auto) — the SwiftUI-style default")
	}

	// The old stretch default must be gone; its return would silently
	// reintroduce implicit equal-width columns.
	if strings.Contains(css, ".mar-hstack > * { flex: 1;") {
		t.Error("hstack must NOT stretch children (flex: 1) — that is the reverted old model")
	}

	// spacer (push siblings apart) and expand (claim free space) are the
	// two explicit distribution tools that replaced implicit stretch.
	if !strings.Contains(css, ".mar-spacer { flex: 1 1 auto") {
		t.Error("spacer must remain the main-axis filler (flex: 1 1 auto)")
	}
	if !strings.Contains(css, ".mar-expand {") {
		t.Error("expand wrapper CSS (.mar-expand) must exist")
	}

	// textField stays greedy on its own (mirroring SwiftUI's TextField),
	// so the canonical `hstack [ textField, button ]` form still works.
	if !strings.Contains(css, ".mar-hstack > .mar-textfield {") {
		t.Error("textField must keep its greedy override inside hstack")
	}
}
