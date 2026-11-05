// Package toollog is poolwarden's own diagnostic log: size-rotating files
// under the state dir, never stdout. Falls back to discard when the dir is
// not writable so dev runs never fail.
package toollog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	pwfs "github.com/smarsh/poolwarden/internal/fs"
	"github.com/smarsh/poolwarden/internal/model"
)

const (
	maxSize  = 5 << 20 // 5 MiB
	keepBack = 3
	name     = "tool.log"
)

// Logger wraps slog with a rotating file writer.
type Logger struct {
	*slog.Logger
	mu    sync.Mutex
	fsys  pwfs.FS
	dir   string
	w     io.Writer
	cur   io.WriteCloser
	size  int64
	seen  map[string]bool
	inner io.Writer // test hook
}

// Open creates tool.log under dir. When the dir is not writable it returns
// (discardLogger, err) — callers may use the logger anyway and ignore err.
func Open(fsys pwfs.FS, dir string) (*Logger, error) {
	l := &Logger{fsys: fsys, dir: dir, seen: map[string]bool{}}
	l.Logger = slog.New(slog.NewTextHandler(writer{l}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if err := l.open(); err != nil {
		l.w = io.Discard
		return l, err
	}
	return l, nil
}

// Discard returns a logger writing nowhere.
func Discard() *Logger {
	l := &Logger{w: io.Discard, seen: map[string]bool{}}
	l.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	return l
}

func (l *Logger) path() string { return l.dir + "/" + name }

func (l *Logger) open() error {
	if l.fsys == nil {
		l.fsys = pwfs.OS{}
	}
	if err := l.fsys.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	f, err := l.fsys.OpenAppend(l.path(), 0o600)
	if err != nil {
		return err
	}
	l.cur = f
	l.w = writer{l}
	if fi, err := l.fsys.Stat(l.path()); err == nil {
		l.size = fi.Size()
	}
	return nil
}

// writer serialises writes through l.mu (slog's handler already serialises,
// but rotation needs the mutex anyway).
type writer struct{ l *Logger }

func (w writer) Write(p []byte) (int, error) {
	w.l.mu.Lock()
	defer w.l.mu.Unlock()
	if w.l.cur == nil {
		return len(p), nil
	}
	if w.l.size+int64(len(p)) > maxSize {
		w.l.rotateLocked()
	}
	n, err := w.l.cur.Write(p)
	w.l.size += int64(n)
	if w.l.inner != nil {
		w.l.inner.Write(p)
	}
	return n, err
}

func (l *Logger) rotateLocked() {
	l.cur.Close()
	l.size = 0
	for i := keepBack; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", l.path(), i)
		if i == keepBack {
			l.fsys.Remove(old)
		} else {
			l.fsys.Rename(fmt.Sprintf("%s.%d", l.path(), i), old)
		}
	}
	l.fsys.Rename(l.path(), l.path()+".1")
	f, err := l.fsys.OpenAppend(l.path(), 0o600)
	if err != nil {
		l.cur = nil
		return
	}
	l.cur = f
}

// Once logs msg at Warn level only the first time key is seen.
func (l *Logger) Once(key, msg string, attrs ...any) {
	l.mu.Lock()
	if l.seen[key] {
		l.mu.Unlock()
		return
	}
	l.seen[key] = true
	l.mu.Unlock()
	l.Warn(msg, attrs...)
}

// Recent reads WARN/ERROR records back from tool.log (source "tool").
func (l *Logger) Recent(_ context.Context, since time.Duration) ([]model.ErrorEvent, error) {
	if l.fsys == nil {
		return nil, nil
	}
	data, err := l.fsys.ReadFile(l.path())
	if err != nil {
		return nil, err
	}
	var out []model.ErrorEvent
	cutoff := time.Now().Add(-since)
	for _, ln := range strings.Split(string(data), "\n") {
		if ln == "" {
			continue
		}
		var ts time.Time
		var level, msg string
		rest := ln
		if strings.HasPrefix(rest, "time=") {
			i := strings.IndexByte(rest, ' ')
			if i < 0 {
				continue
			}
			ts, _ = time.Parse("2006-01-02T15:04:05.999999999Z07:00", strings.TrimPrefix(rest[:i], "time="))
			rest = rest[i+1:]
		}
		if strings.HasPrefix(rest, "level=") {
			i := strings.IndexByte(rest, ' ')
			if i < 0 {
				continue
			}
			level = strings.TrimPrefix(rest[:i], "level=")
			rest = rest[i+1:]
		}
		if strings.HasPrefix(rest, "msg=") {
			msg = strings.Trim(strings.TrimPrefix(rest, "msg="), `"`)
		}
		if level != "WARN" && level != "ERROR" {
			continue
		}
		if ts.Before(cutoff) {
			continue
		}
		out = append(out, model.ErrorEvent{
			TS: ts, Source: "tool", Severity: level, Code: "TOOL",
			Message: msg, Raw: ln,
		})
	}
	return out, nil
}
