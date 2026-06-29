package admin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCatalogDir derives the right path from a DB path.
func TestCatalogDir(t *testing.T) {
	cases := []struct {
		dbPath string
		want   string
	}{
		{"/data/mar.db", "/data/backups"},
		{"./mar.db", "backups"},
		{"/var/lib/notes.db", "/var/lib/backups"},
	}
	for _, tc := range cases {
		got := CatalogDir(tc.dbPath)
		if got != tc.want {
			t.Errorf("CatalogDir(%q): got %q, want %q", tc.dbPath, got, tc.want)
		}
	}
}

// TestListCatalog_Empty — non-existent directory returns nil + no error.
func TestListCatalog_Empty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "no-such")
	got, err := ListCatalog(dir)
	if err != nil {
		t.Fatalf("expected nil error for missing dir; got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list; got %d entries", len(got))
	}
}

// TestListCatalog_NewestFirst — entries come back sorted newest-first.
func TestListCatalog_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	ids := []string{
		"2026-05-08-100000",
		"2026-05-08-120000",
		"2026-05-07-080000", // older
	}
	for _, id := range ids {
		mustTouch(t, filepath.Join(dir, id+".tar.gz"))
	}
	got, err := ListCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries; got %d", len(got))
	}
	// Newest first.
	wantOrder := []string{"2026-05-08-120000", "2026-05-08-100000", "2026-05-07-080000"}
	for i, w := range wantOrder {
		if got[i].ID != w {
			t.Errorf("[%d]: got %q, want %q", i, got[i].ID, w)
		}
	}
}

// TestListCatalog_IgnoresForeignFiles — files that aren't named like
// catalog entries (or aren't .tar.gz) are silently skipped.
func TestListCatalog_IgnoresForeignFiles(t *testing.T) {
	dir := t.TempDir()
	mustTouch(t, filepath.Join(dir, "2026-05-08-100000.tar.gz")) // valid
	mustTouch(t, filepath.Join(dir, "README.md"))                // foreign
	mustTouch(t, filepath.Join(dir, "not-a-timestamp.tar.gz"))   // bad name
	mustTouch(t, filepath.Join(dir, "2026-05-08-100000.txt"))    // wrong ext

	got, err := ListCatalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 valid entry; got %d: %v", len(got), got)
	}
}

// TestPruneCatalog_KeepsNewest — pruning to N keeps the N newest.
func TestPruneCatalog_KeepsNewest(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{
		"2026-05-08-080000",
		"2026-05-08-100000",
		"2026-05-08-120000",
		"2026-05-08-140000",
	} {
		mustTouch(t, filepath.Join(dir, id+".tar.gz"))
	}

	removed, err := PruneCatalog(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Errorf("expected 2 removed; got %d (%v)", len(removed), removed)
	}

	remaining, _ := ListCatalog(dir)
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining; got %d", len(remaining))
	}
	// Newest two should remain.
	if remaining[0].ID != "2026-05-08-140000" || remaining[1].ID != "2026-05-08-120000" {
		t.Errorf("wrong entries remained: %v", remaining)
	}
}

// TestPruneCatalog_NoOpWhenUnderLimit — keep ≥ count is a no-op.
func TestPruneCatalog_NoOpWhenUnderLimit(t *testing.T) {
	dir := t.TempDir()
	mustTouch(t, filepath.Join(dir, "2026-05-08-100000.tar.gz"))

	removed, err := PruneCatalog(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("expected no removals; got %v", removed)
	}
}

// TestNewCatalogID — IDs are UTC, lexicographically sortable.
func TestNewCatalogID(t *testing.T) {
	tm := time.Date(2026, 5, 8, 14, 30, 22, 0, time.UTC)
	got := NewCatalogID(tm)
	want := "2026-05-08-143022"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestIsValidCatalogID — the defense-in-depth shape check the admin
// download/restore handlers use before joining the id into a
// filesystem path. Anything that isn't the exact layout
// YYYY-MM-DD-HHMMSS gets rejected; this includes path-traversal
// attempts, random words, and catalog-shaped strings with invalid
// dates (e.g. month 13).
func TestIsValidCatalogID(t *testing.T) {
	good := []string{
		"2026-05-08-143022",
		"2020-01-01-000000",
		"1970-01-01-000001",
	}
	bad := []string{
		"",
		"random",
		"does-not-exist",
		"abc",
		"../etc/passwd",
		"2020-13-99-XXXX",     // shape-ish but invalid
		"2026-05-08-143022 ",  // trailing space
		" 2026-05-08-143022",  // leading space
		"2026-05-08-143022/x", // injection attempt
		"2026-05-08-14:30:22", // colons, not the layout
	}
	for _, s := range good {
		if !IsValidCatalogID(s) {
			t.Errorf("good id %q rejected", s)
		}
	}
	for _, s := range bad {
		if IsValidCatalogID(s) {
			t.Errorf("bad id %q accepted", s)
		}
	}
}

func mustTouch(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}
