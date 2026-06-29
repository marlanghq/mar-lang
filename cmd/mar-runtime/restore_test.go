// Tests for mar-runtime restore-db.
//
// The CLI is driven through doRestore (the in-process flow) so we can
// inject stdin/stdout/now and assert on the printed output + file
// system state without spawning a subprocess. The runRuntimeRestore
// wrapper is a thin shim around doRestore and gets a smoke test at
// the end.
//
// Each test builds an isolated project directory:
//
//   <tempdir>/
//     mar.json
//     bigapp.db         ← live SQLite, schema known
//     backup.tar.gz     ← bundle produced by admin.WriteSnapshot
//
// then calls doRestore with the appropriate opts and checks the
// outcome. The schema fingerprint comparison is exercised by
// deliberately mutating either side and confirming the CLI refuses.

package main

import (
	"bytes"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"mar/internal/admin"
	"mar/internal/project"
	"mar/internal/runtime"
)

// restoreFixture is the on-disk setup shared by most tests. liveDB
// and backupBundle have matching schemas (so the fingerprint check
// passes by default); tests that want a mismatch mutate one or the
// other before calling doRestore.
type restoreFixture struct {
	projectDir   string
	livePath     string
	backupPath   string
	liveContents []byte // captured for "no change after abort" assertions
}

// newRestoreFixture creates a temp project with a tiny SQLite DB
// (one table) and produces a backup bundle from it. The live DB is
// modified (an insert) between fingerprint capture and bundle
// creation so the bundle contains different bytes — that way "swap
// happened" can be checked by hashing the file after.
func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	dir := t.TempDir()

	// 1. mar.json with a relative DB path. The project loader
	// resolves it against the project dir.
	manifestPath := filepath.Join(dir, "mar.json")
	if err := os.WriteFile(manifestPath,
		[]byte(`{"name":"restoretest","database":{"path":"./bigapp.db"}}`),
		0o644); err != nil {
		t.Fatal(err)
	}

	livePath := filepath.Join(dir, "bigapp.db")

	// 2. Live DB — a couple of tables so fingerprint is non-empty.
	mustCreateDB(t, livePath, "live-original-row")

	// 3. Bundle the current live DB.
	manifest, err := project.LoadManifestStructure(dir)
	if err != nil {
		t.Fatal(err)
	}
	liveDB, err := sql.Open("sqlite", livePath)
	if err != nil {
		t.Fatal(err)
	}
	defer liveDB.Close()
	backupPath := filepath.Join(dir, "backup.tar.gz")
	if err := admin.WriteSnapshot(admin.SnapshotInputs{
		DB:         liveDB,
		Manifest:   manifest,
		ProjectDir: dir,
		OutPath:    backupPath,
		Now:        time.Now().UTC(),
		MarVersion: "test",
	}); err != nil {
		t.Fatal(err)
	}

	// 4. Mutate the live DB so we can later distinguish "bundle
	// applied" from "live untouched".
	if _, err := liveDB.Exec(`INSERT INTO things (note) VALUES ('live-modified-row')`); err != nil {
		t.Fatal(err)
	}
	// Close the handle (so SQLite flushes its WAL) before we read
	// the file bytes for the assertion baseline.
	liveDB.Close()

	liveContents, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}

	return &restoreFixture{
		projectDir:   dir,
		livePath:     livePath,
		backupPath:   backupPath,
		liveContents: liveContents,
	}
}

// mustCreateDB builds a small SQLite DB at path with a known schema
// and one seed row. Used to set up both the live DB and (after
// running WriteSnapshot) the bundle.
func mustCreateDB(t *testing.T, path, seedNote string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE things (id INTEGER PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO things (note) VALUES (?)`, seedNote); err != nil {
		t.Fatal(err)
	}
}

// runDoRestore is the helper that drives doRestore with deterministic
// IO and clock. Returns the exit code, captured stdout, captured
// stderr.
func runDoRestore(t *testing.T, projectDir, bundlePath string, dryRun bool, stdinText string) (int, string, string) {
	t.Helper()
	// Tests release any held lock after each run so the next test
	// can re-acquire on the same path.
	t.Cleanup(runtime.ReleaseHeldDBLock)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := doRestore(restoreOpts{
		projectDir: projectDir,
		bundlePath: bundlePath,
		dryRun:     dryRun,
		stdout:     stdout,
		stderr:     stderr,
		stdin:      strings.NewReader(stdinText),
		now:        func() time.Time { return time.Date(2026, 5, 19, 15, 30, 22, 0, time.UTC) },
	})
	return code, stdout.String(), stderr.String()
}

// TestRestore_Success — happy path. The bundle's DB ends up at the
// live path, the previous file is at .bak-<ts>, exit code 0.
func TestRestore_Success(t *testing.T) {
	f := newRestoreFixture(t)
	code, stdout, stderr := runDoRestore(t, f.projectDir, f.backupPath, false, "restore\n")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Done.") {
		t.Errorf("expected 'Done.' in stdout, got:\n%s", stdout)
	}

	// .bak file exists at the expected timestamp.
	expectedBackup := filepath.Join(f.projectDir, "bigapp.db.bak-2026-05-19T15-30-22")
	if _, err := os.Stat(expectedBackup); err != nil {
		t.Errorf("expected backup file at %s; got %v", expectedBackup, err)
	}

	// The backup matches the pre-restore live contents byte-for-byte.
	gotBackup, err := os.ReadFile(expectedBackup)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBackup, f.liveContents) {
		t.Errorf("backup file contents differ from pre-restore live DB")
	}

	// The new live file is NOT the same as the pre-restore contents
	// (would mean nothing actually swapped).
	gotLive, err := os.ReadFile(f.livePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(gotLive, f.liveContents) {
		t.Errorf("live file unchanged — swap didn't happen")
	}

	// Sanity: open the restored live DB and check it has the
	// bundle's seed row, not the "modified" row we added later.
	db, err := sql.Open("sqlite", f.livePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var note string
	if err := db.QueryRow(`SELECT note FROM things ORDER BY id LIMIT 1`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if note != "live-original-row" {
		t.Errorf("first row note = %q, want %q (bundle's seed)", note, "live-original-row")
	}
}

// TestRestore_DryRun — prints the plan, does not touch the live DB,
// exits 0. Also confirms the lock is released afterward (a second
// dry-run on the same project would otherwise hit ErrDBLocked).
func TestRestore_DryRun(t *testing.T) {
	f := newRestoreFixture(t)
	code, stdout, _ := runDoRestore(t, f.projectDir, f.backupPath, true, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "dry run") {
		t.Errorf("expected 'dry run' marker in stdout:\n%s", stdout)
	}

	// Live file untouched.
	got, _ := os.ReadFile(f.livePath)
	if !bytes.Equal(got, f.liveContents) {
		t.Errorf("dry run modified the live DB")
	}

	// No .bak file created.
	matches, _ := filepath.Glob(filepath.Join(f.projectDir, "bigapp.db.bak-*"))
	if len(matches) != 0 {
		t.Errorf("dry run created backup files: %v", matches)
	}

	// Second dry-run should succeed too (lock was released).
	code2, _, _ := runDoRestore(t, f.projectDir, f.backupPath, true, "")
	if code2 != 0 {
		t.Errorf("second dry-run exit = %d, want 0 (lock not released?)", code2)
	}
}

// TestRestore_SchemaMismatch — bundle's schema fingerprint differs
// from the live DB → exit 1, no files moved, no override.
func TestRestore_SchemaMismatch(t *testing.T) {
	f := newRestoreFixture(t)

	// Add a new table to the live DB — fingerprint diverges.
	{
		db, err := sql.Open("sqlite", f.livePath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE newkid (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	// Re-snapshot the post-mutation live bytes so the "no change"
	// assertion is accurate.
	live2, err := os.ReadFile(f.livePath)
	if err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runDoRestore(t, f.projectDir, f.backupPath, false, "restore\n")
	if code == 0 {
		t.Errorf("expected non-zero exit on schema mismatch")
	}
	if !strings.Contains(stderr, "Schema mismatch") {
		t.Errorf("expected 'Schema mismatch' in stderr:\n%s", stderr)
	}

	// Live file untouched.
	got, _ := os.ReadFile(f.livePath)
	if !bytes.Equal(got, live2) {
		t.Errorf("mismatch path modified the live DB")
	}
	matches, _ := filepath.Glob(filepath.Join(f.projectDir, "bigapp.db.bak-*"))
	if len(matches) != 0 {
		t.Errorf("mismatch path created backup files: %v", matches)
	}
}

// TestRestore_DBLocked — another process (simulated by HoldDBLock)
// holds the lock → restore aborts with the "server is using this
// database" message, no files moved.
func TestRestore_DBLocked(t *testing.T) {
	f := newRestoreFixture(t)

	// Simulate a running server by grabbing the lock first.
	if err := runtime.HoldDBLock(f.livePath); err != nil {
		t.Fatalf("priming lock: %v", err)
	}
	// runDoRestore's cleanup releases at the end of the test.

	code, _, stderr := runDoRestore(t, f.projectDir, f.backupPath, false, "restore\n")
	if code == 0 {
		t.Errorf("expected non-zero exit when DB is locked")
	}
	if !strings.Contains(stderr, "server is using this database") {
		t.Errorf("expected lock-busy message; got:\n%s", stderr)
	}

	got, _ := os.ReadFile(f.livePath)
	if !bytes.Equal(got, f.liveContents) {
		t.Errorf("locked path modified the live DB")
	}
}

// TestRestore_ConfirmationFail — operator types something other than
// "restore" → abort, exit 1, no changes.
func TestRestore_ConfirmationFail(t *testing.T) {
	f := newRestoreFixture(t)
	code, stdout, _ := runDoRestore(t, f.projectDir, f.backupPath, false, "yes\n")
	if code == 0 {
		t.Errorf("expected non-zero exit when confirmation word is wrong")
	}
	if !strings.Contains(stdout, "Aborted") {
		t.Errorf("expected 'Aborted' message; got:\n%s", stdout)
	}

	got, _ := os.ReadFile(f.livePath)
	if !bytes.Equal(got, f.liveContents) {
		t.Errorf("confirmation-fail path modified the live DB")
	}
}

// TestRestore_NoBundle — bundle path doesn't exist → exit 1 before
// any side effects.
func TestRestore_NoBundle(t *testing.T) {
	f := newRestoreFixture(t)
	code, _, stderr := runDoRestore(t, f.projectDir, filepath.Join(f.projectDir, "nope.tar.gz"), false, "restore\n")
	if code == 0 {
		t.Errorf("expected non-zero exit when bundle missing")
	}
	if !strings.Contains(stderr, "bundle") {
		t.Errorf("expected bundle-related error; got:\n%s", stderr)
	}
}

// TestRestore_NoLiveDB — live DB doesn't exist → exit 1 with the
// "extract manually" hint.
func TestRestore_NoLiveDB(t *testing.T) {
	f := newRestoreFixture(t)
	if err := os.Remove(f.livePath); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runDoRestore(t, f.projectDir, f.backupPath, false, "restore\n")
	if code == 0 {
		t.Errorf("expected non-zero exit when live DB missing")
	}
	if !strings.Contains(stderr, "no database at") {
		t.Errorf("expected 'no database at' message; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "tar -xzf") {
		t.Errorf("expected 'tar -xzf' hint for fresh installs; got:\n%s", stderr)
	}
}

// TestRestore_BadBundle — bundle is not a valid tarball → exit 1.
func TestRestore_BadBundle(t *testing.T) {
	f := newRestoreFixture(t)
	// Overwrite the bundle with junk.
	if err := os.WriteFile(f.backupPath, []byte("not a tarball"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runDoRestore(t, f.projectDir, f.backupPath, false, "restore\n")
	if code == 0 {
		t.Errorf("expected non-zero exit on malformed bundle")
	}
	if !strings.Contains(stderr, "bundle") {
		t.Errorf("expected bundle-related error; got:\n%s", stderr)
	}
	got, _ := os.ReadFile(f.livePath)
	if !bytes.Equal(got, f.liveContents) {
		t.Errorf("bad-bundle path modified the live DB")
	}
}

// TestRestore_UnknownFlag — args parsing rejects garbage flags.
func TestRestore_UnknownFlag(t *testing.T) {
	// runRuntimeRestore returns 2 on bad args; we drive it directly
	// here since args parsing is upstream of doRestore.
	origStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	code := runRuntimeRestore([]string{"--force", "x.tar.gz"})
	w.Close()
	os.Stderr = origStderr
	captured, _ := io.ReadAll(r)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", code)
	}
	if !strings.Contains(string(captured), "unknown flag") {
		t.Errorf("expected 'unknown flag' in stderr; got: %s", captured)
	}
}
