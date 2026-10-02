//go:build windows

package owner

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockOffset is where the locked byte is: far past the text of the file, because a locked
// range on Windows also stops another process from reading it.
const lockOffset = 1 << 30

// lockFile takes an exclusive lock on f, and returns ErrHeld when another handle holds one.
func lockFile(f *os.File) error {
	ol := &windows.Overlapped{Offset: lockOffset}
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return ErrHeld
	}
	return err
}
