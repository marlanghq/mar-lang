package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/admin"

	_ "modernc.org/sqlite"
)

// The restore deletes the old database's -wal and -shm, because SQLite would
// replay them over the freshly restored file. It then prints a rollback that
// moves only the .bak back.
//
// Measured before this changed: a WAL holding an uncheckpointed commit is
// where the data IS, so following that rollback returned a database missing
// everything since the last checkpoint — in the probe, `no such table`. The
// rollback path is the one taken when something has already gone wrong, and
// it was the step that lost the data.

// liveWithUncheckpointedCommit builds the situation the finding needs: a
// database in WAL mode whose newest commit has not reached the main file.
// The handle stays open on purpose — closing it checkpoints.
func liveWithUncheckpointedCommit(t *testing.T) (livePath string, close func()) {
	t.Helper()
	live := filepath.Join(t.TempDir(), "live.db")
	db, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE notes (body TEXT)`,
		`INSERT INTO notes VALUES ('written before the restore')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := os.Stat(live + "-wal"); err != nil {
		t.Skipf("this build does not leave a WAL behind: %v", err)
	}
	return live, func() { db.Close() }
}

func TestTheRollbackBringsBackWhatTheWalWasHolding(t *testing.T) {
	live, closeDB := liveWithUncheckpointedCommit(t)
	backup := live + ".bak"

	// What the restore does: move the main file aside — which leaves the
	// sidecars behind — extract the bundle over the live path, then deal
	// with the sidecars.
	if err := os.Rename(live, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("the restored bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	kept := keepSidecarsWithBackup(live, backup)
	closeDB()

	if len(kept) == 0 {
		t.Fatal("the sidecars went nowhere, so the rollback cannot be complete")
	}
	// And the restored database is not left with a foreign WAL, which is
	// the reason they could not simply stay.
	if _, err := os.Stat(live + "-wal"); err == nil {
		t.Fatal("SQLite would replay this over the freshly restored file")
	}

	// Now follow the printed rollback.
	for _, mv := range strings.Split(rollbackCommand(live, backup, kept), " && ") {
		fields := strings.Fields(mv)
		if len(fields) != 3 || fields[0] != "mv" {
			t.Fatalf("the rollback must be plain moves, got %q", mv)
		}
		if err := os.Rename(fields[1], fields[2]); err != nil {
			t.Fatalf("%s: %v", mv, err)
		}
	}

	db, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var body string
	if err := db.QueryRow(`SELECT body FROM notes`).Scan(&body); err != nil {
		t.Fatalf("the rollback lost what the WAL was holding: %v", err)
	}
	if body != "written before the restore" {
		t.Fatalf("got %q", body)
	}
}

func TestTheRollbackIsSimpleWhenThereAreNoSidecars(t *testing.T) {
	// Not every database is in WAL mode, and a checkpointed one has no WAL
	// to move. The command stays the one move it always was.
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	backup := live + ".bak"
	kept := keepSidecarsWithBackup(live, backup)
	if len(kept) != 0 {
		t.Fatalf("nothing to keep, got %v", kept)
	}
	got := rollbackCommand(live, backup, kept)
	if strings.Contains(got, "&&") {
		t.Fatalf("got %q", got)
	}
}

// The live schema fingerprint used to be read with `immutable=1`, which tells
// SQLite the file cannot change — and one thing SQLite does with that is skip
// the WAL. A database whose newest commits had not been checkpointed
// fingerprinted as EMPTY, so restore-db refused it and blamed the operator:
// "the bundle was taken from a database with a different schema". That is the
// state you restore FROM.
func TestTheLiveFingerprintSeesWhatIsInTheWal(t *testing.T) {
	live, closeDB := liveWithUncheckpointedCommit(t)

	// What the main file alone holds, which is what `immutable=1` saw.
	walBefore, err := os.ReadFile(live + "-wal")
	if err != nil {
		t.Fatal(err)
	}

	got, err := fingerprintSQLiteFile(live)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	// The SHA-256 of nothing: what a database with no visible schema gives.
	const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got == emptyDigest {
		t.Fatal("the schema is in the WAL, and the fingerprint did not look there")
	}

	// And --dry-run promises to modify nothing, so the read must not
	// checkpoint: a plain read-write open would fold the WAL into the main
	// file and leave it at zero bytes.
	walAfter, err := os.ReadFile(live + "-wal")
	if err != nil {
		t.Fatalf("the WAL was consumed by a read: %v", err)
	}
	if len(walAfter) != len(walBefore) {
		t.Fatalf("the read checkpointed the WAL: %d bytes -> %d", len(walBefore), len(walAfter))
	}
	closeDB()
}

func TestTheLiveFingerprintMatchesTheOneASnapshotWouldRecord(t *testing.T) {
	// The two halves of the comparison have to be computed the same way, or
	// a restore refuses a bundle taken from the very database it is looking
	// at.
	live, closeDB := liveWithUncheckpointedCommit(t)
	defer closeDB()

	fromFile, err := fingerprintSQLiteFile(live)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+live+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fromDB, err := admin.SchemaFingerprint(db)
	if err != nil {
		t.Fatal(err)
	}
	if fromFile != fromDB {
		t.Fatalf("the same database fingerprints two ways: %.16s vs %.16s", fromFile, fromDB)
	}
}
