package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	pwfs "github.com/smarsh/poolwarden/internal/fs"
)

// LockOwner identifies who holds the cluster lock.
type LockOwner struct {
	Operator string    `json:"operator"`
	Host     string    `json:"host"`
	PID      int       `json:"pid"`
	At       time.Time `json:"at"`
}

// ClusterLock is the local+remote lock for mutating config (01 §9 step 1).
type ClusterLock struct {
	Pool  *Pool
	Cfg   *config.Config
	Clock interface{ Now() time.Time }
	FS    pwfs.FS
	// LocalFlock overrides the local lock primitive (tests inject a noop).
	LocalFlock func(path string) (unlock, lock func() error, err error)
}

const lockStaleAfter = 30 * time.Minute

// LockError reports a held lock.
type LockError struct {
	Owner LockOwner
}

func (e *LockError) Error() string {
	return fmt.Sprintf("cluster locked by %s@%s pid %d since %s",
		e.Owner.Operator, e.Owner.Host, e.Owner.PID,
		e.Owner.At.Format("15:04:05"))
}

// Hint is the standard steal hint.
func (e *LockError) Hint() string {
	return "wait, or --force to steal after 30 min"
}

func (l *ClusterLock) now() time.Time {
	if l.Clock != nil {
		return l.Clock.Now()
	}
	return time.Now()
}

// Acquire takes the local flock and per-host lock dirs. Returns a release
// func. Partial acquire rolls back what was taken.
func (l *ClusterLock) Acquire(ctx context.Context, owner LockOwner, force bool) (func(), error) {
	fsys := l.FS
	if fsys == nil {
		fsys = pwfs.OS{}
	}
	dir := l.Cfg.StateDir
	// local flock
	flock := l.LocalFlock
	if flock == nil {
		flock = lockFuncs
	}
	unlock, lockFn, err := flock(dir + "/poolwarden.lock")
	if err != nil {
		return nil, err
	}
	if err := lockFn(); err != nil {
		return nil, fmt.Errorf("local lock: %w", err)
	}
	owner.At = l.now()
	type hp struct {
		h config.Host
		p string
	}
	var taken []hp
	cleanup := func() {
		for _, t := range taken {
			if sess, err := l.Pool.Session(ctx, t.h); err == nil {
				sess.Exec(ctx, []string{"rm", "-rf", t.p + ".d", t.p}, nil)
			}
		}
		unlock()
	}
	for _, h := range l.Cfg.PgpoolHosts() {
		p := path.Join(dir, "cluster.lock")
		sess, err := l.Pool.Session(ctx, h)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("lock %s: %w", h.Name, err)
		}
		// existing lock?
		res, err := sess.Exec(ctx, []string{"mkdir", p + ".d"}, nil)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("lock %s: %w", h.Name, err)
		}
		if res.Exit != 0 {
			// locked: read owner
			var o LockOwner
			r, _ := sess.Exec(ctx, []string{"cat", path.Join(p, "owner.json")}, nil)
			json.Unmarshal(r.Stdout, &o)
			stale := l.now().Sub(o.At) > lockStaleAfter
			if !(force && stale) {
				cleanup()
				return nil, &LockError{Owner: o}
			}
			// steal
			sess.Exec(ctx, []string{"rm", "-rf", p + ".d"}, nil)
			res, _ = sess.Exec(ctx, []string{"mkdir", p + ".d"}, nil)
			if res.Exit != 0 {
				cleanup()
				return nil, fmt.Errorf("cannot steal lock on %s", h.Name)
			}
		}
		// write owner
		ob, _ := json.Marshal(owner)
		res2, err := sess.Exec(ctx, []string{"mkdir", "-p", p}, nil)
		if err == nil {
			_, err = sess.Exec(ctx,
				[]string{"dd", "of=" + path.Join(p, "owner.json")}, ob)
		}
		if err != nil {
			sess.Exec(ctx, []string{"rm", "-rf", p + ".d"}, nil)
			cleanup()
			return nil, fmt.Errorf("lock %s: %w", h.Name, err)
		}
		_ = res2
		taken = append(taken, hp{h, p})
	}
	// local lock dir too
	lp := path.Join(dir, "cluster.lock")
	fsys.MkdirAll(dir, 0o700)
	ob, _ := json.Marshal(owner)
	fsys.WriteFileAtomic(path.Join(lp, "owner.json"), ob, true)
	return cleanup, nil
}
