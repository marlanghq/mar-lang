package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"mar/internal/runtime"
)

// TestBuildIOS_FrontendOnly guards the topology detection that decides
// whether `mar build --target ios` warns about a missing ios.serverUrl.
// A pure App.frontend app is self-contained (renders from the embedded
// program, calls no Services), so the "no production backend" warning is
// noise and must be suppressed; an App.fullstack app talks to a server,
// so the warning must stand.
//
// The distinction hinges on makeIOSPagesCapture recording kindFullstack
// (not kindFrontend) for the record-shaped App.fullstack call. Before that
// fix both collapsed to kindFrontend and fullstack apps were wrongly
// classified as frontend — this test pins the two apart using real fixtures.
func TestBuildIOS_FrontendOnly(t *testing.T) {
	t.Cleanup(runtime.ResetForReload)

	cases := []struct {
		example          string
		wantFrontendOnly bool
	}{
		{"seasons-gp", true},    // App.frontend — no backend
		{"pet-expenses", false}, // App.fullstack — has a backend
	}

	for _, tc := range cases {
		t.Run(tc.example, func(t *testing.T) {
			dir, err := filepath.Abs(filepath.Join("..", "..", "examples", tc.example))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "Main.mar")); err != nil {
				t.Skipf("%s fixture missing: %v", tc.example, err)
			}

			res, err := BuildIOS(dir, t.TempDir(), "")
			if err != nil {
				t.Fatalf("BuildIOS(%s) failed: %v", tc.example, err)
			}
			if res.FrontendOnly != tc.wantFrontendOnly {
				t.Errorf("%s: FrontendOnly = %v, want %v",
					tc.example, res.FrontendOnly, tc.wantFrontendOnly)
			}
			// Both fixtures ship an ios block without serverUrl, so the
			// missing-URL fact holds for both — what changes is only the
			// presentation gate (FrontendOnly), verified above.
			if !res.MissingServerURL {
				t.Errorf("%s: expected MissingServerURL (fixtures set no serverUrl)", tc.example)
			}
		})
	}
}
