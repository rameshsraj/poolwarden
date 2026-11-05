package clock

import (
	"testing"
	"time"
)

func TestFakeAfter(t *testing.T) {
	f := NewFake(time.Unix(1000, 0))
	ch := f.After(10 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired early")
	default:
	}
	f.Advance(5 * time.Second)
	select {
	case <-ch:
		t.Fatal("fired early")
	default:
	}
	f.Advance(5 * time.Second)
	select {
	case at := <-ch:
		if at != time.Unix(1010, 0) {
			t.Errorf("at = %v", at)
		}
	default:
		t.Fatal("did not fire")
	}
}

func TestFakeTicker(t *testing.T) {
	f := NewFake(time.Unix(1000, 0))
	tk := f.NewTicker(time.Second)
	defer tk.Stop()
	c := tk.C()
	got := 0
	drain := func() {
		for deadline := time.Now().Add(200 * time.Millisecond); time.Now().Before(deadline); {
			select {
			case <-c:
				got++
			default:
				time.Sleep(time.Millisecond)
			}
		}
	}
	f.Advance(2500 * time.Millisecond)
	drain()
	f.Advance(2 * time.Second)
	drain()
	if got < 2 {
		t.Fatalf("ticks = %d, want >=2", got)
	}
}

func TestReal(t *testing.T) {
	r := Real{}
	if r.Now().IsZero() {
		t.Fatal("zero now")
	}
	select {
	case <-r.After(5 * time.Millisecond):
	case <-time.After(time.Second):
		t.Fatal("real After did not fire")
	}
	tk := r.NewTicker(5 * time.Millisecond)
	defer tk.Stop()
	select {
	case <-tk.C():
	case <-time.After(time.Second):
		t.Fatal("real ticker did not fire")
	}
}
