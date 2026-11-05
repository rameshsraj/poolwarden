//go:build unix

package audit

import (
	"os"
	"syscall"
)

// flockPath locks a sidecar file (syscall.Flock, blocking LOCK_EX).
func flockPath(path string) (lock func() error, unlock func() error, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return func() error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) },
		func() error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) },
		nil
}
