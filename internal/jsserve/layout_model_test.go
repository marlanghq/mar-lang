package jsserve

import (
	"strings"
	"testing"
)

// The SwiftUI-style layout model lives in runtime.js as CSS: hstack
// children HUG their content; `spacer` (main axis) and the `width fill`
// / `height fill` attrs are the explicit distribution tools; `align`
// positions hugging children on the cross axis. These assertions pin
// the flip so a later edit can't silently revert hstack to the old
// "stretch every child" (flex: 1) behavior — the source of the
// "alignment is implicit / secret knowledge" problem this model was
// built to fix.
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

	// spacer (push siblings apart) stays the main-axis filler.
	if !strings.Contains(css, ".mar-spacer { flex: 1 1 auto") {
		t.Error("spacer must remain the main-axis filler (flex: 1 1 auto)")
	}

	// textField stays greedy on its own (mirroring SwiftUI's TextField),
	// so the canonical `hstack [ textField, button ]` form still works.
	if !strings.Contains(css, ".mar-hstack > .mar-textfield {") {
		t.Error("textField must keep its greedy override inside hstack")
	}
}

// `width fill` / `height fill` are attr-driven classes whose meaning is
// contextual: on the parent's main axis the child grows as a flex item,
// on the cross axis it stretches. Pins both directions for both axes,
// plus the navigationLink container (the admin panel's table rows fill
// inside it).
func TestFillAttrClasses(t *testing.T) {
	css := runtimeJS

	if !strings.Contains(css, ".mar-hstack > .mar-w-fill,") ||
		!strings.Contains(css, "a.mar-navigation-link > .mar-w-fill { flex: 1 1 0; min-width: 0; }") {
		t.Error("width fill must grow on the hstack/navigationLink main axis (flex: 1 1 0)")
	}
	if !strings.Contains(css, ".mar-vstack > .mar-w-fill { align-self: stretch; }") {
		t.Error("width fill must stretch on the vstack cross axis")
	}
	if !strings.Contains(css, ".mar-vstack > .mar-h-fill,") {
		t.Error("height fill must grow on the vstack main axis")
	}
	if !strings.Contains(css, ".mar-hstack > .mar-h-fill { align-self: stretch; }") {
		t.Error("height fill must stretch on the hstack cross axis")
	}
}

// `align` maps to align-items, scoped per stack axis. A filling child
// overrides via its own stretch rule, so align is position-only.
func TestAlignAttrClasses(t *testing.T) {
	css := runtimeJS

	for _, rule := range []string{
		".mar-vstack.mar-align-leading  { align-items: flex-start; }",
		".mar-vstack.mar-align-center   { align-items: center; }",
		".mar-vstack.mar-align-trailing { align-items: flex-end; }",
		".mar-hstack.mar-align-top      { align-items: flex-start; }",
		".mar-hstack.mar-align-bottom   { align-items: flex-end; }",
	} {
		if !strings.Contains(css, rule) {
			t.Errorf("missing align rule: %s", rule)
		}
	}
}

// `centered` is pure two-axis alignment: it claims the space its
// PARENT provides and never invents a size of its own. The magic
// min-heights (60vh full-page, 160px in-section) are exactly what this
// pins against — sizes must come from the height-propagation chain
// (#mar-root → .mar-nav-stack → .mar-nav-body), not from constants
// baked into the primitive.
func TestCenteredHasNoMagicSize(t *testing.T) {
	css := runtimeJS

	if strings.Contains(css, "60vh") {
		t.Error("centered must not carry a magic min-height (60vh) — it fills what the parent provides")
	}
	if strings.Contains(css, ".mar-section-body > .mar-centered") {
		t.Error("centered must not special-case section nesting with a magic box (160px)")
	}
	if !strings.Contains(css, ".mar-nav-stack { flex: 1 1 auto; display: flex; flex-direction: column; }") {
		t.Error("nav stack must propagate the page height (flex column chain from #mar-root)")
	}
	if !strings.Contains(css, ".mar-nav-body { flex: 1 1 auto; }") {
		t.Error("nav body must propagate the page height down to centered / height-fill children")
	}
}
