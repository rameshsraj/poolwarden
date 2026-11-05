package history

import (
	"context"
	"time"

	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/model"
)

// Collector is what the sampler needs from the collector package.
type Collector interface {
	Collect(ctx context.Context) *model.Snapshot
}

// Sampler periodically appends snapshots to the store (shell/watch only).
type Sampler struct {
	Store    *Store
	Source   Collector
	Interval time.Duration
	Clock    clock.Clock
	OnError  func(error)
}

// Run samples immediately, then every Interval, until ctx is done.
func (s *Sampler) Run(ctx context.Context) {
	iv := s.Interval
	if iv <= 0 {
		iv = 2 * time.Second
	}
	s.sample(ctx)
	var ticks <-chan time.Time
	var stop func()
	if s.Clock != nil {
		tk := s.Clock.NewTicker(iv)
		ticks, stop = tk.C(), tk.Stop
	} else {
		tk := time.NewTicker(iv)
		ticks, stop = tk.C, tk.Stop
	}
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			s.sample(ctx)
		}
	}
}

func (s *Sampler) sample(ctx context.Context) {
	iv := s.Interval
	if iv <= 0 {
		iv = 2 * time.Second
	}
	d := iv
	if d < time.Second {
		d = time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	snap := s.Source.Collect(cctx)
	if snap == nil {
		return
	}
	if err := s.Store.Append(FromSnapshot(snap)); err != nil && s.OnError != nil {
		s.OnError(err)
	}
}
