//go:build unix

package fs

import (
	"os"
	"syscall"
)

// OwnerOf returns the file's numeric owner, or false when it cannot be
// determined on this platform.
func OwnerOf(fi os.FileInfo) (Owner, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return Owner{}, false
	}
	return Owner{UID: int(st.Uid), GID: int(st.Gid)}, true
}
