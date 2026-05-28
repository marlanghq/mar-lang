// DB advisory lock — stub for non-POSIX platforms (Windows).
//
// flock isn't available; we open the file and return the handle
// without locking. The restore CLI's "is the server using this DB?"
// check is therefore weaker on these platforms — the operator must
// ensure the server is stopped before running restore-db. The CLI's
// confirmation prompt already says "the server must not be running",
// so the contract is communicated; we just can't enforce it.

//go:build !unix

package runtime

import (
	"errors"
	"fmt"
	"os"
)

// ErrDBLocked is defined for API parity with the Unix build. On
// non-Unix platforms LockDB never returns it (no detection
// available).
var ErrDBLocked = errors.New("database is locked by another process")

// LockDB on non-Unix platforms opens the sidecar lock-file path
// (`<db>.lock`) for API parity with Unix but does not actually
// acquire any lock. See file-level comment.
func LockDB(path string) (*os.File, error) {
	lockPath := path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("runtime.LockDB: open %s: %w", lockPath, err)
	}
	return f, nil
}
