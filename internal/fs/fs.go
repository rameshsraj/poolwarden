// Package fs is the filesystem abstraction every package that touches disk
// goes through, so all file access can be faked in tests.
package fs

import (
	"io"
	"os"
	"path/filepath"
)

// FS is the minimal filesystem surface poolwarden needs.
type FS interface {
	ReadFile(string) ([]byte, error)
	Stat(string) (os.FileInfo, error)
	WriteFileAtomic(path string, data []byte, keepOwnerMode bool) error
	MkdirAll(string, os.FileMode) error
	Rename(old, new string) error
	Remove(string) error
	ReadDir(string) ([]os.DirEntry, error)
	OpenAppend(path string, mode os.FileMode) (io.WriteCloser, error)
}

// Owner is a numeric file owner.
type Owner struct {
	UID int
	GID int
}

// OS implements FS against the real filesystem.
type OS struct{}

func (OS) ReadFile(p string) ([]byte, error)       { return os.ReadFile(p) }
func (OS) Stat(p string) (os.FileInfo, error)      { return os.Stat(p) }
func (OS) MkdirAll(p string, m os.FileMode) error  { return os.MkdirAll(p, m) }
func (OS) Rename(o, n string) error                { return os.Rename(o, n) }
func (OS) Remove(p string) error                   { return os.Remove(p) }
func (OS) ReadDir(p string) ([]os.DirEntry, error) { return os.ReadDir(p) }
func (OS) OpenAppend(p string, m os.FileMode) (io.WriteCloser, error) {
	return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, m)
}

// WriteFileAtomic writes data via a temp file in the same directory: create,
// write, fsync, chmod/chown to the original file's owner and mode when
// keepOwnerMode and the target exists (otherwise 0600), rename, fsync dir.
func (OS) WriteFileAtomic(path string, data []byte, keepOwnerMode bool) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(e error) error {
		tmp.Close()
		os.Remove(tmpName)
		return e
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	mode := os.FileMode(0o600)
	var owner Owner
	haveOwner := false
	if keepOwnerMode {
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
			owner, haveOwner = OwnerOf(fi)
		}
	}
	if err := tmp.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if haveOwner {
		if err := tmp.Chown(owner.UID, owner.GID); err != nil {
			return cleanup(err)
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
