package runtime

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestMetricsRouteLabelUsesNotFoundForUnknownRoutes(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "metrics-route-label.db"), `
app TodoApi

entity Todo {
  title: String
  authorize all when true
}
`)

	cases := []struct {
		path string
		want string
	}{
		{path: "/favicon.ico", want: "/not-found"},
		{path: "/apple-touch-icon.png", want: "/not-found"},
		{path: "/auth/not-a-real-route", want: "/not-found"},
		{path: "/actions/not/valid", want: "/not-found"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		got := r.metricsRouteLabel(req)
		if got != tc.want {
			t.Fatalf("metricsRouteLabel(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestMetricsRouteLabelPreservesKnownSpecialRoutes(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "metrics-route-label-known.db"), `
app TodoApi

entity Todo {
  title: String
  authorize all when true
}
`)

	cases := []struct {
		path string
		want string
	}{
		{path: "/_mar/backups/download", want: "/_mar/backups/download"},
		{path: "/auth/request-code", want: "/auth/request-code"},
		{path: "/auth/login", want: "/auth/login"},
		{path: "/auth/logout", want: "/auth/logout"},
		{path: "/auth/me", want: "/auth/me"},
		{path: "/actions/sendReminder", want: "/actions/:name"},
		{path: "/todos", want: "/todos"},
		{path: "/todos/123", want: "/todos/:id"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		got := r.metricsRouteLabel(req)
		if got != tc.want {
			t.Fatalf("metricsRouteLabel(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
