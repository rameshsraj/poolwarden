package history

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smarsh/poolwarden/internal/clock"
	"github.com/smarsh/poolwarden/internal/fs/fstest"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/model/modeltest"
)

func mkStore(t0 time.Time) (*Store, *fstest.Fake, *clock.Fake) {
	f := fstest.New()
	c := clock.NewFake(t0)
	return &Store{FS: f, Dir: "/var/lib/poolwarden/samples", Clock: c}, f, c
}

func sampleAt(t time.Time, delay float64) Sample {
	return Sample{
		TS: t,
		Nodes: []NodeSample{
			{Host: "db1", NodeID: 0, Reachable: true, MaxConns: 500, Conns: 80},
			{Host: "db2", NodeID: 1, Reachable: true, InRecovery: true,
				ReplayDelaySec: delay, LagBytes: 128, Conns: 40, MaxConns: 500},
		},
		Children: ChildrenSample{Busy: 5, Total: 100, Clients: 60},
	}
}

func TestFromSnapshot(t *testing.T) {
	sm := FromSnapshot(modeltest.SampleSnapshot())
	if len(sm.Nodes) != 2 {
		t.Fatalf("%+v", sm.Nodes)
	}
	var db2 *NodeSample
	var db1 *NodeSample
	for i := range sm.Nodes {
		if sm.Nodes[i].Host == "db2" {
			db2 = &sm.Nodes[i]
		}
		if sm.Nodes[i].Host == "db1" {
			db1 = &sm.Nodes[i]
		}
	}
	if db2 == nil || db1 == nil {
		t.Fatal("missing nodes")
	}
	if db2.ReplayDelaySec != 2 || db2.LagBytes != 13000 || !db2.InRecovery {
		t.Errorf("%+v", db2)
	}
	if db1.RetainedWALBytes != 1<<20 {
		t.Errorf("retained %d", db1.RetainedWALBytes)
	}
	if db2.HealthFail != 2 || db2.HealthTotal != 100 {
		t.Errorf("health %+v", db2)
	}
}

func TestAppendReadRoundTrip(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	st, _, _ := mkStore(t0)
	if err := st.Append(sampleAt(t0, 2)); err != nil {
		t.Fatal(err)
	}
	got, err := st.Since(time.Hour)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %d", err, len(got))
	}
	if got[0].Nodes[1].ReplayDelaySec != 2 {
		t.Errorf("%+v", got[0])
	}
	l, ok, err := st.Latest()
	if err != nil || !ok || !l.TS.Equal(t0) {
		t.Fatalf("%v %v %+v", err, ok, l)
	}
}

func TestTruncatedLastLineSkipped(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	st, f, _ := mkStore(t0)
	f.Add("/var/lib/poolwarden/samples/current.jsonl",
		`{"ts":"2026-11-05T12:00:00Z","nodes":[],"children":{}}`+"\n"+`{"ts":"2026-11-05T12:0`, 0o600, f.Uid)
	got, err := st.Since(time.Hour)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %d", err, len(got))
	}
}

func TestCompaction(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	st, _, c := mkStore(t0.Add(130 * time.Minute))
	// 70 raw samples 1m apart, all older than now-1h except the last few
	for i := 0; i < 80; i++ {
		if err := st.Append(sampleAt(t0.Add(time.Duration(i)*time.Minute), float64(i))); err != nil {
			t.Fatal(err)
		}
	}
	// the last append triggered compaction already; force check via Since
	raw, _ := st.readFile("current.jsonl")
	arc, _ := st.readFile("archive.jsonl")
	if len(arc) == 0 {
		t.Fatal("no archive buckets")
	}
	if len(raw) == 0 {
		t.Fatal("raw should still have recent samples")
	}
	for _, sm := range raw {
		if c.Now().Sub(sm.TS) > st.DownsampleAfter {
			t.Errorf("old raw sample kept: %v", sm.TS)
		}
	}
	// archive buckets are 1m averages
	for _, sm := range arc {
		if len(sm.Nodes) != 2 {
			t.Errorf("bucket nodes %+v", sm.Nodes)
		}
	}
}

func TestRetentionDrop(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	st, f, c := mkStore(t0)
	st.Retention = time.Hour
	st.DownsampleAfter = 10 * time.Minute
	old := t0.Add(-2 * time.Hour)
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString(fmt.Sprintf(`{"ts":%q,"nodes":[],"children":{}}`+"\n", old.Add(time.Duration(i)*time.Minute).Format(time.RFC3339)))
	}
	f.Add("/var/lib/poolwarden/samples/archive.jsonl", b.String(), 0o600, f.Uid)
	c.Set(t0)
	if err := st.Compact(); err != nil {
		t.Fatal(err)
	}
	arc, _ := st.readFile("archive.jsonl")
	if len(arc) != 0 {
		t.Fatalf("old archive kept: %d", len(arc))
	}
}

func TestMaxBytesCap(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	st, _, _ := mkStore(t0)
	st.MaxBytes = 400
	for i := 0; i < 10; i++ {
		if err := st.Append(sampleAt(t0.Add(time.Duration(i)*time.Second), float64(i))); err != nil {
			t.Fatal(err)
		}
	}
	fi, _ := st.FS.Stat("/var/lib/poolwarden/samples/current.jsonl")
	if fi == nil || fi.Size() > 400 {
		t.Fatalf("size %v", fi)
	}
}

func TestSinceWindow(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	st, _, c := mkStore(t0)
	for i := 0; i < 5; i++ {
		st.Append(sampleAt(t0.Add(time.Duration(i-5)*time.Minute), float64(i)))
	}
	c.Set(t0)
	got, _ := st.Since(2 * time.Minute)
	for _, sm := range got {
		if sm.TS.Before(t0.Add(-2 * time.Minute)) {
			t.Errorf("sample too old: %v", sm.TS)
		}
	}
	if len(got) == 0 {
		t.Fatal("empty window")
	}
	span, _ := st.Span()
	if span != 4*time.Minute {
		t.Errorf("span %v", span)
	}
}

func TestSampler(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	st, _, c := mkStore(t0)
	src := &fakeSource{snap: modeltest.SampleSnapshot()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Sampler{Store: st, Source: src, Interval: time.Second, Clock: c}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	waitCount := func(n int) bool {
		for src.count() < n && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		return src.count() >= n
	}
	// fake ticker coalesces like a real ticker: one tick per Advance;
	// advances before the ticker registers are lost, so keep advancing
	for time.Now().Before(deadline) && src.count() < 6 {
		c.Advance(time.Second)
		waitCount(src.count() + 1)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler did not stop")
	}
	if src.count() < 6 {
		t.Fatalf("samples %d", src.count())
	}
	raw, _ := st.readFile("current.jsonl")
	if len(raw) < 6 {
		t.Fatalf("stored %d", len(raw))
	}
}

type fakeSource struct {
	snap *model.Snapshot
	n    int64
}

func (f *fakeSource) Collect(context.Context) *model.Snapshot {
	atomic.AddInt64(&f.n, 1)
	return f.snap
}

func (f *fakeSource) count() int { return int(atomic.LoadInt64(&f.n)) }
