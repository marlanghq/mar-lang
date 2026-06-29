package iosbundle

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestIOSRendererCoversEmittedViewTags catches the class of bug where a
// UI builtin constructs a MarView with a new `tag` (so user code
// compiles and the builtins-drift test passes) but MarRenderer has no
// matching `case`, so the primitive silently renders nothing on iOS.
// That is exactly how paragraph, textArea, and picker each shipped
// broken at one point.
//
// Source of truth: every `MarView(tag: "X")` constructed anywhere in
// the Swift sources. MarRenderer.swift must handle each tag with a
// `case "X"` — except tags a parent renderer consumes directly rather
// than through the top-level switch (listed in consumedByParent).
func TestIOSRendererCoversEmittedViewTags(t *testing.T) {
	dir := filepath.Join("template", "Sources")

	emitted, err := emittedViewTags(dir)
	if err != nil {
		t.Fatalf("scanning emitted tags: %v", err)
	}
	// Sanity guard: if the scan regexes ever stop matching, an empty
	// emitted set would make this test pass vacuously. The UI
	// vocabulary has well over a dozen view tags.
	if len(emitted) < 15 {
		t.Fatalf("emitted-tag scan looks broken: only %d tags found", len(emitted))
	}
	handled, err := rendererCaseTags(filepath.Join(dir, "MarRenderer.swift"))
	if err != nil {
		t.Fatalf("scanning renderer cases: %v", err)
	}

	// Tags consumed by a parent renderer, with no top-level case:
	//   span — folded into the paragraph case's AttributedString.
	consumedByParent := map[string]bool{"span": true}

	var missing []string
	for tag := range emitted {
		if consumedByParent[tag] || handled[tag] {
			continue
		}
		missing = append(missing, tag)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("view tag(s) emitted by a builtin but unhandled in MarRenderer "+
			"(would render blank on iOS): %s\n"+
			"add a `case \"<tag>\"` to MarRenderer.swift, or add it to "+
			"consumedByParent if a parent handles it.",
			strings.Join(missing, ", "))
	}
}

var (
	// Leaf views: `MarView(tag: "X")`.
	reEmittedTag = regexp.MustCompile(`MarView\(\s*tag:\s*"([a-zA-Z]+)"`)
	// Structural views built via the container factories
	// (`container("hstack")`, `contentOnlyContainer("form")`), where the
	// tag is the factory's literal argument rather than a MarView field.
	reContainerTag = regexp.MustCompile(`(?:container|contentOnlyContainer)\(\s*"([a-zA-Z]+)"`)
	reQuotedWord   = regexp.MustCompile(`"([a-zA-Z]+)"`)
)

// emittedViewTags returns the set of view tags produced anywhere under
// the Sources dir, whether constructed directly as `MarView(tag:)` or
// via the container factories.
func emittedViewTags(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".swift") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, re := range []*regexp.Regexp{reEmittedTag, reContainerTag} {
			for _, m := range re.FindAllSubmatch(data, -1) {
				out[string(m[1])] = true
			}
		}
	}
	return out, nil
}

// rendererCaseTags returns every quoted tag on a `case ...:` line in
// MarRenderer.swift. Line-based so combined cases
// (`case "uiSection", "uiKeyedList":`) contribute every tag. Extra
// entries from the input-kind / inline-attr switches are harmless —
// the test only checks that emitted tags are a subset of handled ones.
func rendererCaseTags(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "case ") {
			continue
		}
		for _, m := range reQuotedWord.FindAllStringSubmatch(line, -1) {
			out[m[1]] = true
		}
	}
	return out, nil
}
