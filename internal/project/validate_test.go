package project

import (
	"strings"
	"testing"
)

// TestValidate_NilManifest pins the no-op behavior. Useful because
// LoadManifest returns nil for projects without mar.json.
func TestValidate_NilManifest(t *testing.T) {
	if err := Validate(nil, CompileTime); err != nil {
		t.Fatalf("expected no error for nil manifest; got %v", err)
	}
}

// TestValidate_AdminsAcceptsValidEmails confirms the happy path.
// Multiple addresses, mixed providers, all should pass.
func TestValidate_AdminsAcceptsValidEmails(t *testing.T) {
	m := &Manifest{
		Admins: []string{"me@example.com", "ops@team.io", "a.b+tag@domain.co.uk"},
	}
	if err := Validate(m, CompileTime); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidate_AdminsRejectsBadShape covers obvious garbage. We're
// not RFC-strict, just catching things the user clearly didn't mean.
func TestValidate_AdminsRejectsBadShape(t *testing.T) {
	cases := []struct {
		name  string
		email string
	}{
		{"empty", ""},
		{"no @", "notanemail"},
		{"no domain", "user@"},
		{"no local", "@domain.com"},
		{"no tld", "user@domain"},
		{"spaces", "user @example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{Admins: []string{tc.email}}
			err := Validate(m, CompileTime)
			if err == nil {
				t.Fatalf("expected error for %q; got nil", tc.email)
			}
			if !strings.Contains(err.Error(), "admins[0]") {
				t.Errorf("error should reference admins[0]; got %v", err)
			}
		})
	}
}

// TestValidate_AdminsRejectsDuplicates — the boot-time sync would
// silently dedupe, but accepting it at parse means the user could
// have a typo (same email twice with different casing) the check
// missed. Compile-time rejection forces them to fix it.
func TestValidate_AdminsRejectsDuplicates(t *testing.T) {
	m := &Manifest{
		Admins: []string{"me@x.com", "ops@x.com", "me@x.com"},
	}
	err := Validate(m, CompileTime)
	if err == nil {
		t.Fatal("expected duplicate error; got nil")
	}
	if !strings.Contains(err.Error(), "duplicates") {
		t.Errorf("error should mention duplicate; got %v", err)
	}
}

// TestValidate_RecentRequestsSizeAcceptsRange covers the boundaries
// + typical values. 0 is "missing" (gets default), so it's allowed
// even though it's outside the [10, 5000] range.
func TestValidate_RecentRequestsSizeAcceptsRange(t *testing.T) {
	cases := []int{0, 10, 200, 1000, 5000}
	for _, v := range cases {
		m := &Manifest{AdminPanel: &AdminPanelConfig{RecentRequestsSize: v}}
		if err := Validate(m, CompileTime); err != nil {
			t.Errorf("recentRequestsSize=%d: unexpected error %v", v, err)
		}
	}
}

// TestValidate_RecentRequestsSizeRejectsOutOfRange — the whole point
// of hard rejection is catching surprises like 99999 at compile
// time, not silently clamping to 5000.
func TestValidate_RecentRequestsSizeRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name  string
		value int
	}{
		{"too small", 5},
		{"too large", 99999},
		{"negative", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{AdminPanel: &AdminPanelConfig{RecentRequestsSize: tc.value}}
			err := Validate(m, CompileTime)
			if err == nil {
				t.Fatalf("expected error for value=%d; got nil", tc.value)
			}
			if !strings.Contains(err.Error(), "recentRequestsSize") {
				t.Errorf("error should reference field; got %v", err)
			}
		})
	}
}

// TestResolvedRecentRequestsSize pins the default-fallback behavior.
// 0 / nil receiver / nil AdminPanel all yield the documented default;
// explicit values pass through verbatim.
func TestResolvedRecentRequestsSize(t *testing.T) {
	cases := []struct {
		name string
		m    *Manifest
		want int
	}{
		{"nil manifest", nil, 200},
		{"no adminPanel", &Manifest{}, 200},
		{"explicit zero", &Manifest{AdminPanel: &AdminPanelConfig{}}, 200},
		{"explicit 50", &Manifest{AdminPanel: &AdminPanelConfig{RecentRequestsSize: 50}}, 50},
		{"explicit 5000", &Manifest{AdminPanel: &AdminPanelConfig{RecentRequestsSize: 5000}}, 5000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolvedRecentRequestsSize(tc.m)
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}
