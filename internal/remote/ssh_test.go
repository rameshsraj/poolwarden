package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/smarsh/poolwarden/internal/config"
)

// startExecServer starts a minimal in-process SSH server that accepts
// clientPub and answers "exec" requests with a fixed payload.
func startExecServer(t *testing.T, clientPub ssh.PublicKey) (addr string, hostKey ssh.PublicKey) {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(clientPub.Marshal()) {
				return nil, nil
			}
			return nil, os.ErrPermission
		},
	}
	cfg.AddHostKey(hostSigner)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					nc.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "session" {
						ch.Reject(ssh.UnknownChannelType, "no")
						continue
					}
					c, rqs, _ := ch.Accept()
					go func(c ssh.Channel) {
						defer c.Close()
						for rq := range rqs {
							if rq.Type == "exec" {
								rq.Reply(true, nil)
								c.Write([]byte("executed\n"))
								c.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
								return
							}
							rq.Reply(false, nil)
						}
					}(c)
				}
			}()
		}
	}()
	return ln.Addr().String(), hostSigner.PublicKey()
}

// sshFixture returns a config.Host (with the listener's host:port in Address)
// and SSH config pointing at a generated client key.
func sshFixture(t *testing.T, writeKnownHosts bool) (config.Host, config.SSH) {
	t.Helper()
	_, clientPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPriv)
	if err != nil {
		t.Fatal(err)
	}
	addr, hostKey := startExecServer(t, clientSigner.PublicKey())
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	blk, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(blk), 0o600); err != nil {
		t.Fatal(err)
	}
	sshCfg := config.SSH{
		Key:        keyFile,
		KnownHosts: filepath.Join(t.TempDir(), "known_hosts"),
		Timeout:    5 * time.Second,
	}
	line := ""
	if writeKnownHosts {
		line = knownhosts.Line([]string{addr}, hostKey) + "\n"
	}
	if err := os.WriteFile(sshCfg.KnownHosts, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Host{Name: "db2", Address: addr, SSHUser: "root"}, sshCfg
}

func TestSSHKnownHostsMissing(t *testing.T) {
	h, sshCfg := sshFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, step, err := SSHDialer{}.Dial(ctx, h, sshCfg)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "knownhosts") && !strings.Contains(err.Error(), "handshake") {
		t.Errorf("err %v", err)
	}
	if step == nil || step.Result != "failed" {
		t.Errorf("step %+v", step)
	}
}

func TestSSHExecRoundTrip(t *testing.T) {
	h, sshCfg := sshFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, step, err := SSHDialer{}.Dial(ctx, h, sshCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if step == nil || step.Result != "ok" {
		t.Errorf("step %+v", step)
	}
	res, err := sess.Exec(ctx, []string{"echo", "hi"}, nil)
	if err != nil || res.Exit != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if string(res.Stdout) != "executed\n" {
		t.Errorf("stdout %q", res.Stdout)
	}
}

func TestSSHSessionOutlivesDialCtx(t *testing.T) {
	h, sshCfg := sshFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	sess, _, err := SSHDialer{}.Dial(ctx, h, sshCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	cancel() // the dial ctx ends; the session must survive
	res, err := sess.Exec(context.Background(), []string{"echo", "hi"}, nil)
	if err != nil || res.Exit != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if string(res.Stdout) != "executed\n" {
		t.Errorf("stdout %q", res.Stdout)
	}
}
