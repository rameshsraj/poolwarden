package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/exitcode"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
)

// SSHDialer opens real SSH sessions: key auth only, strict known_hosts, the
// configured timeout, and ctx enforced by closing the connection.
type SSHDialer struct {
	Now func() time.Time
}

func (d SSHDialer) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d SSHDialer) Dial(ctx context.Context, h config.Host, cfg config.SSH) (Session, *Step, error) {
	step := &Step{Host: h.Name, SSHUser: h.SSHUser, Action: "connect", Start: d.now()}
	started := d.now()
	sess, err := d.dial(ctx, h, cfg)
	step.End = d.now()
	step.ConnectMS = step.End.Sub(started).Milliseconds()
	if err != nil {
		step.Result = "failed"
		step.Err = err.Error()
		return nil, step, err
	}
	step.Result = "ok"
	return sess, step, nil
}

func (d SSHDialer) dial(ctx context.Context, h config.Host, cfg config.SSH) (Session, error) {
	keyData, err := os.ReadFile(cfg.Key)
	if err != nil {
		return nil, exitcode.New(exitcode.Connection, "cannot read SSH key").
			WithCause(err.Error()).WithHint("check ssh.key in the config")
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		var perr *ssh.PassphraseMissingError
		if errors.As(err, &perr) {
			return nil, exitcode.New(exitcode.Connection, "SSH key is passphrase-protected").
				WithCause(cfg.Key).
				WithHint("use a passphrase-less deploy key for poolwarden")
		}
		return nil, exitcode.New(exitcode.Connection, "cannot parse SSH key").
			WithCause(err.Error())
	}
	hkc, err := knownhosts.New(cfg.KnownHosts)
	if err != nil {
		return nil, exitcode.New(exitcode.Connection, "cannot load known_hosts").
			WithCause(err.Error()).WithHint("add the host key to " + cfg.KnownHosts)
	}
	user := h.SSHUser
	if user == "" {
		user = "root"
	}
	cc := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hkc,
		Timeout:         cfg.Timeout,
	}
	addr := h.Address
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "22")
	}
	dialer := net.Dialer{Timeout: cfg.Timeout}
	nc, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, exitcode.New(exitcode.Connection, "cannot reach "+h.Name).
			WithCause(err.Error()).WithHint("poolwarden config self-check")
	}
	conn, chans, reqs, err := ssh.NewClientConn(nc, addr, cc)
	if err != nil {
		var kerr *knownhosts.KeyError
		if errors.As(err, &kerr) {
			return nil, exitcode.New(exitcode.Connection, "SSH handshake failed on "+h.Name).
				WithCause(err.Error()).
				WithHint("ssh-keyscan -H " + h.Address + " >> " + cfg.KnownHosts)
		}
		nc.Close()
		return nil, exitcode.New(exitcode.Connection, "SSH handshake failed on "+h.Name).
			WithCause(err.Error())
	}
	client := ssh.NewClient(conn, chans, reqs)
	return &sshSession{client: client, host: h.Name}, nil
}

type sshSession struct {
	client *ssh.Client
	host   string
}

func (s *sshSession) Exec(ctx context.Context, args []string, stdin []byte) (ExecResult, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return ExecResult{Exit: -1}, err
	}
	defer sess.Close()
	var out, errb bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errb
	sess.Stdin = bytes.NewReader(stdin)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			sess.Close()
		case <-done:
		}
	}()
	err = sess.Run(Quote(args))
	if cerr := ctx.Err(); cerr != nil {
		return ExecResult{Exit: -1}, cerr
	}
	res := ExecResult{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			res.Exit = ee.ExitStatus()
			return res, nil
		}
		res.Exit = -1
		return res, err
	}
	return res, nil
}

// ReadFile cats the remote file; missing files surface as an error.
// ExecStream streams a remote command's stdout; ctx cancel kills it.
func (s *sshSession) ExecStream(ctx context.Context, args []string, w io.Writer) error {
	sess, err := s.client.NewSession()
	if err != nil {
		return err
	}
	sess.Stdout = w
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sess.Signal(ssh.SIGKILL)
			sess.Close()
		case <-done:
		}
	}()
	defer close(done)
	if err := sess.Start(Quote(args)); err != nil {
		return err
	}
	err = sess.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (s *sshSession) ReadFile(ctx context.Context, path string) ([]byte, error) {
	res, err := s.Exec(ctx, []string{"cat", "--", path}, nil)
	if err != nil {
		return nil, err
	}
	if res.Exit != 0 {
		return nil, fmt.Errorf("cat %s: %s", path, strings.TrimSpace(string(res.Stderr)))
	}
	return res.Stdout, nil
}

// WriteFileAtomic implements docs/04 I23: dd to a temp file in the same dir,
// sync, chmod/chown --reference to the target when it exists, mv, sync dir.
func (s *sshSession) WriteFileAtomic(ctx context.Context, path string, data []byte) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp := dir + "/." + base + ".tmp-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	steps := [][]string{
		{"dd", "of=" + tmp},
		{"sync", tmp},
	}
	// chmod: reference the target's mode when it exists, else 0600.
	if _, err := s.Stat(ctx, path); err == nil {
		steps = append(steps,
			[]string{"chmod", "--reference=" + path, tmp},
			[]string{"chown", "--reference=" + path, tmp})
	} else {
		steps = append(steps, []string{"chmod", "0600", tmp})
	}
	steps = append(steps,
		[]string{"mv", "-f", tmp, path},
		[]string{"sync", dir})
	for i, argv := range steps {
		var stdin []byte
		if i == 0 {
			stdin = data
		}
		res, err := s.Exec(ctx, argv, stdin)
		if err != nil || res.Exit != 0 {
			s.Exec(ctx, []string{"rm", "-f", tmp}, nil)
			if err != nil {
				return err
			}
			return fmt.Errorf("remote write %s failed at %v: %s", path, argv, strings.TrimSpace(string(res.Stderr)))
		}
	}
	return nil
}

func (s *sshSession) Stat(ctx context.Context, path string) (FileInfo, error) {
	res, err := s.Exec(ctx, []string{"stat", "-c", "%u %g %a %s", "--", path}, nil)
	if err != nil {
		return FileInfo{}, err
	}
	if res.Exit != 0 {
		return FileInfo{}, fmt.Errorf("stat %s: %s", path, strings.TrimSpace(string(res.Stderr)))
	}
	var uid, gid int
	var mode, size int64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(res.Stdout)), "%d %d %o %d", &uid, &gid, &mode, &size); err != nil {
		return FileInfo{}, fmt.Errorf("stat %s: unparseable %q", path, res.Stdout)
	}
	return FileInfo{Mode: os.FileMode(mode), Size: size, Owner: pwfs.Owner{UID: uid, GID: gid}}, nil
}

func (s *sshSession) Close() error { return s.client.Close() }
