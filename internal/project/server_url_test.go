package project

import (
	"strings"
	"testing"
)

// `ios.serverUrl` is the base the phone builds every request from, as
// `URL(string: "/api/notes", relativeTo: base)`. An absolute-path reference
// REPLACES the base's path (RFC 3986 §5.3), so a base with a path has that
// path silently thrown away. Measured with Foundation itself:
//
//	https://x.example.com/sub  +  /api/notes  ->  https://x.example.com/api/notes
//
// The manifest accepted it, the phone ignored it, and the developer got 404s
// with nothing to point at. A trailing slash resolves identically to no path
// at all, so that one stays allowed.
func TestServerURLCannotCarryAPathThePhoneWouldDrop(t *testing.T) {
	manifest := func(url string) *Manifest {
		return &Manifest{Name: "x", Locale: "en", IOS: &IOSConfig{
			BundleID: "com.e.x", DisplayName: "X",
			MarketingVersion: "1.0", BuildNumber: "1", ServerURL: url,
		}}
	}

	refused := []string{
		"https://x.example.com/sub",
		"https://x.example.com/sub/",
		"http://localhost:3000/api",
	}
	for _, url := range refused {
		t.Run(url, func(t *testing.T) {
			err := Validate(manifest(url))
			if err == nil {
				t.Fatal("accepted; the phone would drop the path and answer 404")
			}
			if !strings.Contains(err.Error(), "has the path") {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
		})
	}

	accepted := []string{
		"https://x.example.com",
		"https://x.example.com/", // resolves the same as no path
		"http://localhost:3000",
		"http://localhost:3000/",
		"", // no backend
	}
	for _, url := range accepted {
		t.Run("accepts "+url, func(t *testing.T) {
			if err := Validate(manifest(url)); err != nil {
				t.Fatalf("this is an ordinary base URL: %v", err)
			}
		})
	}

	// The scheme rule still comes first: it is the more specific complaint.
	t.Run("the scheme is still checked", func(t *testing.T) {
		err := Validate(manifest("http://example.com/sub"))
		if err == nil || !strings.Contains(err.Error(), "must be https://") {
			t.Fatalf("expected the scheme complaint, got %v", err)
		}
	})
}
