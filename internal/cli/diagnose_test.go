package cli

import (
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/diagnose"
	"github.com/smarsh/poolwarden/internal/model"
	"github.com/smarsh/poolwarden/internal/model/modeltest"
)

// snapFiring returns the sample snapshot mutated so STANDBY_NOT_STREAMING
// fires (db2 missing from pg_stat_replication) alongside the standing
// WD_HALF_VOTES_2NODE warn.
func snapFiring() *model.Snapshot {
	s := modeltest.SampleSnapshot()
	s.Backends[0].Standbys = nil
	return s
}

func TestDiagnoseGolden(t *testing.T) {
	d, out, errb := viewDeps(t, nil)
	d.Source = fakeSource{snap: snapFiring()}
	d.Diagnose = diagnose.NewEngine()
	if code := Execute([]string{"diagnose"}, d); code != 11 {
		t.Fatalf("code %d stderr %s out %s", code, errb, out)
	}
	goldenView(t, "diagnose.table.golden", out.String())
}

func TestDiagnoseSeverityFilter(t *testing.T) {
	d, out, _ := viewDeps(t, nil)
	d.Source = fakeSource{snap: snapFiring()}
	d.Diagnose = diagnose.NewEngine()
	if code := Execute([]string{"diagnose", "--severity", "crit"}, d); code != 11 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out.String(), "STANDBY_NOT_STREAMING") || strings.Contains(out.String(), "WD_HALF_VOTES") {
		t.Errorf("filtered out: %s", out.String())
	}
}

func TestExplainGolden(t *testing.T) {
	d, out, errb := viewDeps(t, nil)
	d.Source = fakeSource{snap: snapFiring()}
	d.Diagnose = diagnose.NewEngine()
	if code := Execute([]string{"explain", "STANDBY_NOT_STREAMING:db2"}, d); code != 0 {
		t.Fatalf("code %d stderr %s", code, errb)
	}
	goldenView(t, "explain.table.golden", out.String())
}

func TestExplainNotFiring(t *testing.T) {
	d, out, _ := viewDeps(t, nil)
	d.Source = fakeSource{snap: modeltest.SampleSnapshot()}
	d.Diagnose = diagnose.NewEngine()
	if code := Execute([]string{"explain", "SPLIT_BRAIN:cluster"}, d); code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out.String(), "not currently firing") {
		t.Errorf("out %s", out.String())
	}
}

func TestExplainUnknown(t *testing.T) {
	d, _, errb := viewDeps(t, nil)
	if code := Execute([]string{"explain", "NOPE:x"}, d); code != 1 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errb.String(), "poolwarden diagnose") {
		t.Errorf("stderr %s", errb.String())
	}
}

func TestDiagnoseWarnExit(t *testing.T) {
	d, _, _ := viewDeps(t, warnFinding())
	d.Diagnose = fakeDiagnose{findings: warnFinding()}
	if code := Execute([]string{"diagnose"}, d); code != 10 {
		t.Fatalf("code %d", code)
	}
}
