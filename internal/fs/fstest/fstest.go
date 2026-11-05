// Package fstest provides an in-memory fs.FS for tests. Files carry a mode
// and an owner so permission checks behave like the real filesystem.
package fstest

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	pwfs "github.com/smarsh/poolwarden/internal/fs"
)

type File struct {
	Data  []byte
	Mode  os.FileMode
	Owner pwfs.Owner
}

type Fake struct {
	FailOpenAppend bool

	mu    sync.Mutex
	Files map[string]*File
	// Uid used for files created by write operations.
	Uid int
}

func New() *Fake {
	return &Fake{Files: map[string]*File{}, Uid: os.Getuid()}
}

// Add seeds a regular file.
func (f *Fake) Add(path, content string, mode os.FileMode, uid int) {
	path = filepath.Clean(path)
	f.ensureDir(filepath.Dir(path))
	f.Files[path] = &File{Data: []byte(content), Mode: mode, Owner: pwfs.Owner{UID: uid, GID: uid}}
}

func (f *Fake) ensureDir(dir string) {
	dir = filepath.Clean(dir)
	if dir == "/" || dir == "." {
		return
	}
	f.ensureDir(filepath.Dir(dir))
	if _, ok := f.Files[dir]; !ok {
		f.Files[dir] = &File{Mode: os.ModeDir | 0o755}
	}
}

func norm(path string) string { return filepath.Clean(path) }

func (f *Fake) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.Files[norm(path)]
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	if fl.Mode.IsDir() {
		return nil, &os.PathError{Op: "read", Path: path, Err: errors.New("is a directory")}
	}
	return append([]byte(nil), fl.Data...), nil
}

type fileInfo struct {
	name string
	mode os.FileMode
	size int64
}

func (fi fileInfo) Name() string       { return fi.name }
func (fi fileInfo) Size() int64        { return fi.size }
func (fi fileInfo) Mode() os.FileMode  { return fi.mode }
func (fi fileInfo) ModTime() time.Time { return time.Time{} }
func (fi fileInfo) IsDir() bool        { return fi.mode.IsDir() }
func (fi fileInfo) Sys() any           { return nil }

// Stat returns info carrying the file's owner; use StatOwner to read it.
func (f *Fake) Stat(path string) (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.Files[norm(path)]
	if !ok {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
	}
	return fakeInfo{fileInfo{filepath.Base(path), fl.Mode, int64(len(fl.Data))}, fl.Owner}, nil
}

type fakeInfo struct {
	fileInfo
	owner pwfs.Owner
}

func (fi fakeInfo) Sys() any {
	return &syscall.Stat_t{Uid: uint32(fi.owner.UID), Gid: uint32(fi.owner.GID)}
}

func (f *Fake) WriteFileAtomic(path string, data []byte, keepOwnerMode bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = norm(path)
	mode := os.FileMode(0o600)
	owner := pwfs.Owner{UID: f.Uid, GID: f.Uid}
	if keepOwnerMode {
		if old, ok := f.Files[path]; ok {
			mode = old.Mode
			owner = old.Owner
		}
	}
	parent := filepath.Dir(path)
	if _, ok := f.Files[parent]; !ok {
		return &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
	}
	f.Files[path] = &File{Data: append([]byte(nil), data...), Mode: mode, Owner: owner}
	return nil
}

func (f *Fake) MkdirAll(path string, mode os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = norm(path)
	f.ensureDir(filepath.Dir(path))
	f.Files[path] = &File{Mode: os.ModeDir | mode, Owner: pwfs.Owner{UID: f.Uid, GID: f.Uid}}
	return nil
}

func (f *Fake) Rename(old, new string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	old = norm(old)
	fl, ok := f.Files[old]
	if !ok {
		return &os.PathError{Op: "rename", Path: old, Err: os.ErrNotExist}
	}
	f.Files[norm(new)] = fl
	delete(f.Files, old)
	return nil
}

func (f *Fake) Remove(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = norm(path)
	if _, ok := f.Files[path]; !ok {
		return &os.PathError{Op: "remove", Path: path, Err: os.ErrNotExist}
	}
	delete(f.Files, path)
	return nil
}

type dirEntry struct{ fileInfo }

func (d dirEntry) Type() fs.FileMode          { return d.mode.Type() }
func (d dirEntry) Info() (fs.FileInfo, error) { return d.fileInfo, nil }

func (f *Fake) ReadDir(path string) ([]os.DirEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = norm(path)
	d, ok := f.Files[path]
	if !ok || !d.Mode.IsDir() {
		return nil, &os.PathError{Op: "readdir", Path: path, Err: os.ErrNotExist}
	}
	prefix := path + "/"
	var out []os.DirEntry
	for p, fl := range f.Files {
		if p == path || !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := p[len(prefix):]
		if strings.ContainsRune(rest, '/') {
			continue
		}
		out = append(out, dirEntry{fileInfo{rest, fl.Mode, int64(len(fl.Data))}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (f *Fake) OpenAppend(path string, mode os.FileMode) (io.WriteCloser, error) {
	if f.FailOpenAppend {
		return nil, fmt.Errorf("injected OpenAppend failure")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path = norm(path)
	if _, ok := f.Files[path]; !ok {
		f.Files[path] = &File{Mode: mode, Owner: pwfs.Owner{UID: f.Uid, GID: f.Uid}}
	}
	return &appendWriter{f: f, path: path}, nil
}

type appendWriter struct {
	f    *Fake
	path string
}

func (w *appendWriter) Write(p []byte) (int, error) {
	w.f.mu.Lock()
	defer w.f.mu.Unlock()
	fl := w.f.Files[w.path]
	fl.Data = append(fl.Data, p...)
	return len(p), nil
}

func (w *appendWriter) Close() error { return nil }

var _ = fmt.Sprintf
var _ pwfs.FS = (*Fake)(nil)
