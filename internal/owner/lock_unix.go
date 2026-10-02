//go:build !windows

package owner

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive lock on f, and returns ErrHeld when another open file holds one.
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrHeld
	}
	return err
}
