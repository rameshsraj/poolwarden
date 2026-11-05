package remote

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"

	"github.com/smarsh/poolwarden/internal/config"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
)

// LocalSession implements Session against the local host: commands run via
// exec (no shell) and files via internal/fs.
type LocalSession struct {
	FS pwfs.FS
}

func (s *LocalSession) fsys() pwfs.FS {
	if s.FS == nil {
		return pwfs.OS{}
	}
	return s.FS
}

func (s *LocalSession) Exec(ctx context.Context, args []string, stdin []byte) (ExecResult, error) {
	if len(args) == 0 {
		return ExecResult{Exit: -1}, exec.ErrNotFound
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	res := ExecResult{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.Exit = ee.ExitCode()
			return res, nil
		}
		res.Exit = -1
		return res, err
	}
	return res, nil
}

// ExecStream streams a local command's stdout; ctx cancel kills the child.
func (s *LocalSession) ExecStream(ctx context.Context, args []string, w io.Writer) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		return err
	}
	err := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (s *LocalSession) ReadFile(_ context.Context, path string) ([]byte, error) {
	return s.fsys().ReadFile(path)
}

func (s *LocalSession) WriteFileAtomic(_ context.Context, path string, data []byte) error {
	return s.fsys().WriteFileAtomic(path, data, true)
}

func (s *LocalSession) Stat(_ context.Context, path string) (FileInfo, error) {
	fi, err := s.fsys().Stat(path)
	if err != nil {
		return FileInfo{}, err
	}
	owner, _ := pwfs.OwnerOf(fi)
	return FileInfo{Mode: fi.Mode(), Size: fi.Size(), Owner: owner}, nil
}

func (s *LocalSession) Close() error { return nil }

// IsLocal reports whether h refers to the machine poolwarden runs on, by
// matching its address against local interface addresses or the hostname.
func IsLocal(h config.Host) bool {
	local := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if hn, err := os.Hostname(); err == nil {
		local[hn] = true
	}
	if addrs, err := interfaceAddrs(); err == nil {
		for _, a := range addrs {
			local[a] = true
		}
	}
	return local[h.Address] || local[h.Name]
}

func interfaceAddrs() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ip := a.String()
			for i, c := range ip {
				if c == '/' {
					ip = ip[:i]
					break
				}
			}
			out = append(out, ip)
		}
	}
	return out, nil
}
