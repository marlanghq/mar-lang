// DB advisory lock — POSIX flock implementation.
//
// Used by `mar dev` / `mar-runtime` on startup (held for the lifetime
// of the process) and by `mar-runtime restore-db` to detect whether a
// server is currently using the database before performing the swap.
//
// Why flock instead of a PID file: the kernel guarantees release on
// process exit regardless of how the process died (clean shutdown,
// SIGKILL, panic, OOM kill, power loss). There's no stale-lockfile
// recovery path to get wrong — the lock is a property of the open
// file descriptor.
//
// Why a sidecar lock file (`<db>.lock`) instead of locking the DB
// file directly: on macOS, BSD flock(2) and POSIX fcntl(F_SETLK)
// locks share the kernel's per-vnode lock list in practice. Holding
// flock(LOCK_EX) on the DB file blocks SQLite's own fcntl-based
// write locks when the runtime later runs `BEGIN IMMEDIATE` during
// migrations or normal writes. The sidecar sidesteps this entirely:
// SQLite never opens `<db>.lock`, so its fcntl locks live on a
// different inode and never collide with our flock.
//
// The lock file is created empty (0o600) on demand; its only purpose
// is to host the advisory lock. Contents are irrelevant.

//go:build unix

package runtime

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrDBLocked indicates that another process already holds the
// exclusive advisory lock on the database.
var ErrDBLocked = errors.New("database is locked by another process")

// lockFilePath returns the sidecar lock-file path for a given DB
// path. Convention: same directory, same basename, `.lock` suffix.
// (E.g. `/srv/app/mar.db` → `/srv/app/mar.db.lock`.) Kept in one
// helper so the server and the restore CLI always agree on the
// location.
func lockFilePath(dbPath string) string {
	return dbPath + ".lock"
}

// LockDB acquires an exclusive non-blocking advisory lock for the
// SQLite database at path. The lock is held on a sidecar file
// (`<path>.lock`) so it doesn't interfere with SQLite's own
// internal locking. Returns the open file holding the lock;
// callers must keep the handle open for as long as they want the
// lock held. Closing the file releases; the kernel also releases
// on process exit, regardless of how the process died.
//
// The DB file itself does not need to exist — the sidecar is
// created on demand. This lets the runtime acquire the lock
// before SQLite has bootstrapped the schema.
//
// Returns ErrDBLocked if another process already holds the lock.
func LockDB(path string) (*os.File, error) {
	lockPath := lockFilePath(path)
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("runtime.LockDB: open %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrDBLocked
		}
		return nil, fmt.Errorf("runtime.LockDB: flock %s: %w", lockPath, err)
	}
	return f, nil
}
