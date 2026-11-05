// Package clock abstracts time so collectors, samplers and audit code can be
// driven deterministically in tests.
package clock

import (
	"sync"
	"time"
)

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
	NewTicker(time.Duration) Ticker
}

type Real struct{}

func (Real) Now() time.Time                         { return time.Now() }
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (Real) NewTicker(d time.Duration) Ticker       { return realTicker{time.NewTicker(d)} }

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }

// Fake is a manually-advanced clock. Pending After timers and tickers fire
// when Advance moves time past their deadline.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	pending []*fakeTimer
	tickers []*fakeTicker
}

type fakeTimer struct {
	at   time.Time
	ch   chan time.Time
	stop chan struct{}
	once sync.Once
}

func NewFake(start time.Time) *Fake { return &Fake{now: start} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.afterAtLocked(f.now.Add(d))
}

func (f *Fake) afterAtLocked(at time.Time) <-chan time.Time {
	t := &fakeTimer{at: at, ch: make(chan time.Time, 1), stop: make(chan struct{})}
	f.pending = append(f.pending, t)
	f.fireLocked()
	return t.ch
}

func (f *Fake) NewTicker(d time.Duration) Ticker {
	t := &fakeTicker{ch: make(chan time.Time, 1), stop: make(chan struct{})}
	f.mu.Lock()
	t.at = f.now.Add(d)
	t.d = d
	t.clk = f
	f.tickers = append(f.tickers, t)
	f.mu.Unlock()
	return t
}

// Set jumps the clock to t and fires any timers that came due.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
	f.fireLocked()
}

// Advance moves the clock forward and fires any timers that came due.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	f.fireLocked()
}

func (f *Fake) fireLocked() {
	for _, t := range f.pending {
		if t.at.After(f.now) {
			continue
		}
		t.once.Do(func() {
			select {
			case t.ch <- t.at:
			default:
			}
		})
	}
	var active []*fakeTicker
	for _, tk := range f.tickers {
		select {
		case <-tk.stop:
			continue
		default:
		}
		for !tk.at.After(f.now) {
			at := tk.at
			select {
			case tk.ch <- at:
			default:
			}
			tk.at = tk.at.Add(tk.d)
		}
		active = append(active, tk)
	}
	f.tickers = active
}

type fakeTicker struct {
	clk  *Fake
	at   time.Time
	d    time.Duration
	ch   chan time.Time
	stop chan struct{}
	once sync.Once
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }

func (t *fakeTicker) Stop() {
	t.once.Do(func() {
		close(t.stop)
		t.clk.mu.Lock()
		t.clk.fireLocked()
		t.clk.mu.Unlock()
	})
}
