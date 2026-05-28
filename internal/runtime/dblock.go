// DB lock holder — keeps the *os.File returned by LockDB alive for
// the process lifetime, so GC doesn't close the underlying fd and
// release the flock prematurely.
//
// Server startup (mar / mar-runtime runFromPath, mar dev) calls
// HoldDBLock once the DB file exists; the lock survives until the
// process exits, at which point the kernel releases it.
//
// Short-lived CLIs that just need to peek (mar-runtime backup,
// admin list) deliberately skip this — they coexist with a running
// server. Restore is the only operation that *requires* exclusivity,
// and it calls LockDB directly (holding a local *os.File for the
// duration of the swap).

package runtime

import "os"

var heldDBLock *os.File

// HoldDBLock acquires an exclusive advisory lock on the database
// file at path and stashes the handle in a package var for the
// process lifetime. Idempotent — subsequent calls with the same
// path are no-ops.
//
// Returns ErrDBLocked if another process holds the lock (e.g. a
// second `mar dev` against the same project, or a restore CLI in
// progress).
func HoldDBLock(path string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	if heldDBLock != nil {
		return nil
	}
	f, err := LockDB(path)
	if err != nil {
		return err
	}
	heldDBLock = f
	return nil
}

// ReleaseHeldDBLock closes the held lock file, releasing the flock.
// Mostly for tests; production processes just exit and let the
// kernel handle it.
func ReleaseHeldDBLock() {
	dbMu.Lock()
	defer dbMu.Unlock()
	if heldDBLock != nil {
		_ = heldDBLock.Close()
		heldDBLock = nil
	}
}
