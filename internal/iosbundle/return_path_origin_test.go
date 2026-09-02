package iosbundle

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The browser guards the path it captures on a 401 with isSafeReturnPath: a
// person can type a URL, so `?next=` is an open-redirect surface and the guard
// is what closes it. The phone captures currentPath with NO guard, which reads
// like the same hole until you ask where the value comes from.
//
// It comes from navPath, which the runtime writes and nothing else: currentPath
// is `navPath.last ?? "/"`, and navPath is only appended or replaced by
// navigate(), reached from Nav.push and linkTo -- paths the PROGRAM builds and
// the app's own route table matched. Measured: the template registers no
// URL-opening entry point at all, so no outside string reaches it.
//
// That makes the asymmetry correct rather than missing, and it is safe by an
// ABSENCE, which is the kind of safety that disappears quietly. So the absence
// is what this watches. Add a deep link, a custom scheme, a universal link or a
// widget entry point, and an outside string can reach navPath -- at which point
// the phone needs the browser's guard, and this test is where that gets said.
func TestNoOutsideStringReachesTheCapturedReturnPath(t *testing.T) {
	// The ways UIKit and SwiftUI hand an app a URL from elsewhere.
	entryPoints := regexp.MustCompile(
		`onOpenURL|openURLContexts|application\(_: *UIApplication, *open|` +
			`continue *userActivity|NSUserActivityTypeBrowsingWeb|onContinueUserActivity|` +
			`widgetURL|\.handlesExternalEvents`)

	files, err := filepath.Glob(filepath.Join("template", "Sources", "*.swift"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 15 {
		t.Fatalf("only %d Swift sources found; the glob is wrong, not the template", len(files))
	}

	var found []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "///") {
				continue
			}
			if entryPoints.MatchString(trimmed) {
				found = append(found, filepath.Base(f)+":"+itoa(i+1)+": "+trimmed)
			}
		}
	}
	sort.Strings(found)
	if len(found) > 0 {
		t.Errorf("the app now takes a path from outside:\n  %s\n\n"+
			"currentPath is captured into pendingReturnPath on a 401 and navigated to after\n"+
			"sign-in. While every path came from the program that was safe; a URL someone else\n"+
			"can produce makes it the browser's ?next= surface, and the phone has no\n"+
			"isSafeReturnPath. Port that guard, or state here why this entry point cannot\n"+
			"reach navPath.", strings.Join(found, "\n  "))
	}

	// And the premise: currentPath still reads from navPath rather than from
	// somewhere a URL could set. If this stops being true the reasoning above
	// is void even with no entry point.
	ctx, err := os.ReadFile(filepath.Join("template", "Sources", "MarAppContext.swift"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ctx), `navPath.last ?? "/"`) {
		t.Error("currentPath no longer reads from navPath; the reason the phone needs no " +
			"return-path guard was that the runtime is the only writer")
	}
}
