// Package remotetest provides fake Session/Dialer implementations for tests.
package remotetest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/remote"
)

// Call records one Session method invocation.
type Call struct {
	Method string
	Args   []string
	Path   string
}

// FakeSession is a scripted Session.
type FakeSession struct {
	mu    sync.Mutex
	Calls []Call
	Files map[string][]byte
	// Execs maps "arg0 arg1 ..." prefixes to results; "" is the catch-all.
	Execs map[string]remote.ExecResult
	// Block makes Exec wait for ctx cancellation (dead-source tests).
	Block bool
	Err   error
	// StreamOut is written by ExecStream when StreamBlock is false.
	StreamOut   []byte
	StreamBlock bool // ExecStream blocks until ctx done
}

func NewSession() *FakeSession {
	return &FakeSession{Files: map[string][]byte{}, Execs: map[string]remote.ExecResult{}}
}

func (f *FakeSession) Exec(ctx context.Context, args []string, stdin []byte) (remote.ExecResult, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, Call{Method: "exec", Args: args})
	f.mu.Unlock()
	if f.Block {
		<-ctx.Done()
		return remote.ExecResult{Exit: -1}, ctx.Err()
	}
	if f.Err != nil {
		return remote.ExecResult{Exit: -1}, f.Err
	}
	key := strings.Join(args, " ")
	for k, r := range f.Execs {
		if k == "" {
			continue
		}
		pre, suf := strings.HasPrefix(k, "*"), strings.HasSuffix(k, "*")
		switch {
		case k == key,
			pre && suf && strings.Contains(key, k[1:len(k)-1]),
			suf && strings.HasPrefix(key, k[:len(k)-1]),
			pre && strings.HasSuffix(key, k[1:]):
			return r, nil
		}
	}
	if r, ok := f.Execs[""]; ok {
		return r, nil
	}
	// Emulate cat/stat on the file map for convenience.
	if len(args) >= 2 && args[0] == "cat" {
		if d, ok := f.Files[args[len(args)-1]]; ok {
			return remote.ExecResult{Stdout: d}, nil
		}
		return remote.ExecResult{Exit: 1, Stderr: []byte("No such file")}, nil
	}
	return remote.ExecResult{Exit: 127, Stderr: []byte("unscripted exec: " + key)}, nil
}

func (f *FakeSession) ReadFile(_ context.Context, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, Call{Method: "read", Path: path})
	if d, ok := f.Files[path]; ok {
		return d, nil
	}
	return nil, os.ErrNotExist
}

func (f *FakeSession) WriteFileAtomic(_ context.Context, path string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, Call{Method: "write", Path: path})
	f.Files[path] = append([]byte(nil), data...)
	return nil
}

func (f *FakeSession) Stat(_ context.Context, path string) (remote.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.Files[path]; ok {
		return remote.FileInfo{Mode: 0o600, Size: int64(len(d))}, nil
	}
	return remote.FileInfo{}, os.ErrNotExist
}

func (f *FakeSession) Close() error { return nil }

// FakeDialer returns scripted sessions per host name.
type FakeDialer struct {
	mu       sync.Mutex
	Sessions map[string]*FakeSession
	Steps    []*remote.Step
	Err      error
	// Block makes Dial wait for ctx cancellation.
	Block bool
}

func NewDialer() *FakeDialer {
	return &FakeDialer{Sessions: map[string]*FakeSession{}}
}

func (d *FakeDialer) Dial(ctx context.Context, h config.Host, _ config.SSH) (remote.Session, *remote.Step, error) {
	step := &remote.Step{Host: h.Name, SSHUser: h.SSHUser, Action: "connect", Start: time.Now()}
	if d.Block {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	step.End = step.Start.Add(time.Millisecond)
	if d.Err != nil {
		step.Result = "failed"
		step.Err = d.Err.Error()
		return nil, step, d.Err
	}
	s, ok := d.Sessions[h.Name]
	if !ok {
		s = NewSession()
		d.Sessions[h.Name] = s
	}
	step.Result = "ok"
	d.Steps = append(d.Steps, step)
	return s, step, nil
}

var _ remote.Dialer = (*FakeDialer)(nil)
var _ remote.Session = (*FakeSession)(nil)
var _ = fmt.Sprintf
var _ = filepath.Dir

// ExecStream writes StreamOut to w, or blocks until ctx when StreamBlock.
func (s *FakeSession) ExecStream(ctx context.Context, args []string, w io.Writer) error {
	if s.StreamBlock {
		<-ctx.Done()
		return ctx.Err()
	}
	_, err := w.Write(s.StreamOut)
	return err
}
