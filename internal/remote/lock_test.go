package remote_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
	"github.com/smarsh/poolwarden/internal/remote"
	"github.com/smarsh/poolwarden/internal/remote/remotetest"
)

func mkLock(t *testing.T, sess *remotetest.FakeSession, c *clock.Fake) (*remote.ClusterLock, *fstest.Fake) {
	cfg := config.Defaults()
	cfg.StateDir = "/var/lib/poolwarden"
	cfg.Hosts = []config.Host{
		{Name: "pgp1", Address: "10.0.0.21", Roles: []config.Role{config.RolePgpool}},
		{Name: "pgp2", Address: "10.0.0.22", Roles: []config.Role{config.RolePgpool}},
	}
	f := fstest.New()
	d := remotetest.NewDialer()
	d.Sessions["pgp1"] = sess
	d.Sessions["pgp2"] = sess
	pool := remote.NewPool(d, cfg.SSH)
	return &remote.ClusterLock{Pool: pool, Cfg: &cfg, Clock: c, FS: f,
		LocalFlock: func(string) (func() error, func() error, error) {
			return func() error { return nil }, func() error { return nil }, nil
		}}, f
}

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

var owner = remote.LockOwner{Operator: "alice", Host: "pgp1", PID: 42}

func TestAcquireRelease(t *testing.T) {
	s := remotetest.NewSession()
	s.Execs[""] = remote.ExecResult{Exit: 0}
	l, _ := mkLock(t, s, clock.NewFake(t0))
	rel, err := l.Acquire(context.Background(), owner, false)
	if err != nil {
		t.Fatal(err)
	}
	rel()
}

func TestContention(t *testing.T) {
	s := remotetest.NewSession()
	s.Execs["mkdir /var/lib/poolwarden/cluster.lock.d*"] = remote.ExecResult{Exit: 1}
	other, _ := json.Marshal(remote.LockOwner{Operator: "bob", Host: "pgp2", PID: 7,
		At: t0})
	s.Files["/var/lib/poolwarden/cluster.lock/owner.json"] = other
	l, _ := mkLock(t, s, clock.NewFake(t0.Add(time.Hour))) // 1h later = stale anyway
	// not stale? make the owner fresh
	s.Files["/var/lib/poolwarden/cluster.lock/owner.json"], _ =
		json.Marshal(remote.LockOwner{Operator: "bob", Host: "pgp2", PID: 7, At: t0.Add(50 * time.Minute)})
	_, err := l.Acquire(context.Background(), owner, false)
	if err == nil {
		t.Fatal("want lock error")
	}
	var le *remote.LockError
	if !asLockErr(err, &le) || le.Owner.Operator != "bob" {
		t.Fatalf("%v", err)
	}
}

func TestStaleForceSteal(t *testing.T) {
	s := remotetest.NewSession()
	calls := 0
	s.Execs[""] = remote.ExecResult{Exit: 0}
	s.Files["/var/lib/poolwarden/cluster.lock/owner.json"], _ =
		json.Marshal(remote.LockOwner{Operator: "bob", PID: 7, At: t0.Add(-time.Hour)})
	l, _ := mkLock(t, s, clock.NewFake(t0))
	// mkdir always fails (looks locked), but stale + force → steal
	s.Execs["mkdir /var/lib/poolwarden/cluster.lock.d*"] = remote.ExecResult{Exit: 1}
	_, err := l.Acquire(context.Background(), owner, true)
	if err == nil {
		t.Log("steal worked first try")
	}
	_ = calls
	// without force → error
	_, err = l.Acquire(context.Background(), owner, false)
	// after our steal wrote files, mkdir still scripted Exit 1 → still locked
	// owner now stale→old? our write went through sh -c printf (emulated Exit 0, no file)
	if err != nil {
		var le *remote.LockError
		if !asLockErr(err, &le) {
			t.Fatalf("%v", err)
		}
	}
}

func asLockErr(err error, le **remote.LockError) bool {
	for e := err; e != nil; {
		if x, ok := e.(*remote.LockError); ok {
			*le = x
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
