//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockInstance takes an exclusive OS-level lock on the given lock file.
// The lock is tied to the open handle, so it is released automatically by
// the OS when the process dies or exits — including on a crash — so it can
// never go stale like a flag stored in a file would.
// The returned release function is safe to call once.
func lockInstance(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	var overlapped windows.Overlapped
	err = windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped,
	)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errAnotherInstance
		}
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	return func() { _ = f.Close() }, nil
}
