//go:build unix

package remote

import (
	"os"
	"syscall"
)

// lockFuncs returns (unlock, lock) for a local flock on path.
func lockFuncs(path string) (func() error, func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return func() error {
			err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			f.Close()
			return err
		},
		func() error {
			return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		}, nil
}
