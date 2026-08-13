package admin

// Regression tests for audit finding #9 (backup files readable by
// other local users): the snapshot catalog must be 0o700 and every
// bundle 0o600, including catalogs created before the policy.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func skipIfNoUnixPerms(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful on windows")
	}
}

// TestWriteSnapshot_OwnerOnlyPerms: a fresh snapshot into a
// not-yet-existing catalog dir must leave the dir 0o700 and the
// bundle 0o600. The bundle is a full copy of the database, so a
// group/world-readable mode would hand every table (sessions, auth
// codes) to any local user on a shared host.
func TestWriteSnapshot_OwnerOnlyPerms(t *testing.T) {
	skipIfNoUnixPerms(t)

	db := openTestDB(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "mar.json"),
		[]byte(`{"name":"perms-test","locale":"en"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog := filepath.Join(t.TempDir(), "database-backups")
	outPath := filepath.Join(catalog, "backup-perms.tar.gz")
	if err := WriteSnapshot(SnapshotInputs{
		DB:         db,
		Manifest:   nil,
		ProjectDir: project,
		OutPath:    outPath,
		Now:        time.Unix(1_700_000_000, 0),
		MarVersion: "test",
	}); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	dirInfo, err := os.Stat(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("catalog dir mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("bundle mode = %o, want 600", got)
	}
}

// TestEnsureBackupDir_TightensExisting: catalogs created before the
// 0o700 policy sit at 0o755 on disk; MkdirAll alone would leave them
// that way forever, so ensureBackupDir must chmod them down.
func TestEnsureBackupDir_TightensExisting(t *testing.T) {
	skipIfNoUnixPerms(t)

	dir := filepath.Join(t.TempDir(), "old-catalog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureBackupDir(dir); err != nil {
		t.Fatalf("ensureBackupDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("pre-existing dir mode = %o, want 700", got)
	}
}
