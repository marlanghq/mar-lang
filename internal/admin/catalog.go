// Backup catalog — the directory of automatic + ad-hoc snapshots
// kept alongside the live database on the same volume. In Fly
// production, catalog lives at `<dir-of-mar.db>/backups/`; the Fly
// volume snapshot covers it automatically.
//
// Catalog filename convention:
//
//   <YYYY-MM-DD-HHMMSS>.tar.gz
//
// The timestamp uses UTC; sortable lexicographically. ID stripping
// drops the `.tar.gz` so the public-facing identifier in API URLs
// stays human-readable.
//
// Bundle shape inside each tarball is the same as Phase 4 backup:
// metadata.json + mar.json + mar.db. Both auto and ad-hoc backups
// produce the same shape, so restore doesn't need to distinguish.

package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CatalogDir derives the catalog path from the live mar.db path.
// /data/mar.db → /data/backups/. Doesn't create the directory; the
// scheduler does that on first write.
func CatalogDir(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "backups")
}

// CatalogEntry is one file in the catalog. Surfaced via the admin
// panel API. SchemaFingerprint and other metadata live INSIDE the
// tarball — Entry just describes what's on disk for listing.
type CatalogEntry struct {
	ID        string    // filename without .tar.gz extension, e.g. "2026-05-08-143022"
	Path      string    // absolute path to the tarball
	SizeBytes int64
	CreatedAt time.Time // parsed from the filename timestamp
}

// catalogIDLayout is the strftime-style format for catalog filenames.
// UTC, dash-separated for filesystem-safety. Lexicographic order =
// chronological order.
const catalogIDLayout = "2006-01-02-150405"

// NewCatalogID returns an ID for a backup taken at `now`. UTC.
func NewCatalogID(now time.Time) string {
	return now.UTC().Format(catalogIDLayout)
}

// CatalogPath returns the full filesystem path for the given ID
// inside the catalog directory.
func CatalogPath(catalogDir, id string) string {
	return filepath.Join(catalogDir, id+".tar.gz")
}

// ListCatalog returns all backup entries in the catalog, newest
// first. Returns an empty slice if the directory doesn't exist yet
// (auto-backup hasn't run; ad-hoc never invoked).
func ListCatalog(catalogDir string) ([]CatalogEntry, error) {
	entries, err := os.ReadDir(catalogDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("admin.ListCatalog: %w", err)
	}
	out := make([]CatalogEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		id := strings.TrimSuffix(name, ".tar.gz")
		t, err := time.Parse(catalogIDLayout, id)
		if err != nil {
			continue // foreign file in the catalog — skip silently
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, CatalogEntry{
			ID:        id,
			Path:      filepath.Join(catalogDir, name),
			SizeBytes: info.Size(),
			CreatedAt: t.UTC(),
		})
	}
	// Newest first — the panel's UI lists most recent at top.
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// PruneCatalog deletes the oldest entries until at most `keep`
// remain. Returns the IDs removed. Idempotent — passing keep >= len
// is a no-op.
//
// Called by the scheduler after each successful new backup.
func PruneCatalog(catalogDir string, keep int) ([]string, error) {
	entries, err := ListCatalog(catalogDir)
	if err != nil {
		return nil, err
	}
	if len(entries) <= keep {
		return nil, nil
	}
	// entries is newest-first; the tail (oldest) is what gets removed.
	removed := make([]string, 0, len(entries)-keep)
	for _, e := range entries[keep:] {
		if err := os.Remove(e.Path); err != nil {
			return removed, fmt.Errorf("admin.PruneCatalog: %w", err)
		}
		removed = append(removed, e.ID)
	}
	return removed, nil
}

// MostRecentCatalogEntry returns the single newest entry, or
// (CatalogEntry{}, false) if the catalog is empty. Used by the
// scheduler's skip-if-recent check.
func MostRecentCatalogEntry(catalogDir string) (CatalogEntry, bool, error) {
	entries, err := ListCatalog(catalogDir)
	if err != nil {
		return CatalogEntry{}, false, err
	}
	if len(entries) == 0 {
		return CatalogEntry{}, false, nil
	}
	return entries[0], true, nil
}
