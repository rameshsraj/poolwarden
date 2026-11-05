// Package remote runs commands and file operations on cluster hosts, over
// SSH for peers and directly for the local host. SSH is the only place a
// command string is built (sshd always runs it through the login shell), and
// every argument goes through Quote.
package remote

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
)

// Host is one inventory target (a subset of config.Host).
type Host struct {
	Name    string
	Address string
	User    string
}

// Step is the audit-steps[] timing record for one remote action.
type Step struct {
	Host      string
	SSHUser   string
	Action    string
	ConnectMS int64
	Start     time.Time
	End       time.Time
	Result    string
	Err       string
	Output    string // trimmed stdout+stderr, max 10 lines
}

// ExecResult is one command's output.
type ExecResult struct {
	Stdout []byte
	Stderr []byte
	Exit   int
}

// FileInfo is the subset of file metadata poolwarden checks remotely.
type FileInfo struct {
	Mode  os.FileMode
	Size  int64
	Owner pwfs.Owner
}

// Session is one open connection to a host (or the local host).
type Session interface {
	Exec(ctx context.Context, args []string, stdin []byte) (ExecResult, error)
	// ExecStream runs args streaming stdout to w until exit or ctx cancel
	// (the remote/local process is killed on cancel).
	ExecStream(ctx context.Context, args []string, w io.Writer) error
	ReadFile(ctx context.Context, path string) ([]byte, error)
	WriteFileAtomic(ctx context.Context, path string, data []byte) error
	Stat(ctx context.Context, path string) (FileInfo, error)
	Close() error
}

// Dialer opens sessions; each Dial reports its connect timing as a Step.
type Dialer interface {
	Dial(ctx context.Context, h config.Host, cfg config.SSH) (Session, *Step, error)
}

// Quote joins args into a remote command line using POSIX single-quote
// escaping: 'foo' and embedded quotes become '\” sequences.
func Quote(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += "'" + squote(a) + "'"
	}
	return out
}

func squote(s string) string {
	q := ""
	for _, r := range s {
		if r == '\'' {
			q += `'\''`
		} else {
			q += string(r)
		}
	}
	return q
}

// LazySession is a Session that dials on first use with the caller's ctx
// (never at construction). Session failure is memoised for the session.
type LazySession struct {
	Pool *Pool
	Host config.Host
	once sync.Once
	sess Session
	err  error
}

func (l *LazySession) get(ctx context.Context) (Session, error) {
	l.once.Do(func() {
		l.sess, l.err = l.Pool.Session(ctx, l.Host)
	})
	return l.sess, l.err
}

func (l *LazySession) Exec(ctx context.Context, args []string, stdin []byte) (ExecResult, error) {
	s, err := l.get(ctx)
	if err != nil {
		return ExecResult{Exit: -1}, err
	}
	return s.Exec(ctx, args, stdin)
}

func (l *LazySession) ExecStream(ctx context.Context, args []string, w io.Writer) error {
	s, err := l.get(ctx)
	if err != nil {
		return err
	}
	return s.ExecStream(ctx, args, w)
}

func (l *LazySession) ReadFile(ctx context.Context, path string) ([]byte, error) {
	s, err := l.get(ctx)
	if err != nil {
		return nil, err
	}
	return s.ReadFile(ctx, path)
}

func (l *LazySession) WriteFileAtomic(ctx context.Context, path string, data []byte) error {
	s, err := l.get(ctx)
	if err != nil {
		return err
	}
	return s.WriteFileAtomic(ctx, path, data)
}

func (l *LazySession) Stat(ctx context.Context, path string) (FileInfo, error) {
	s, err := l.get(ctx)
	if err != nil {
		return FileInfo{}, err
	}
	return s.Stat(ctx, path)
}

func (l *LazySession) Close() error {
	if l.sess == nil {
		return nil
	}
	return l.sess.Close()
}
