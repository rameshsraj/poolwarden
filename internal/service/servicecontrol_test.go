package service

import (
	"context"
	"strings"
	"testing"

	"github.com/smarsh/poolwarden/internal/model/modeltest"
)

func TestServiceControlStopPgpool(t *testing.T) {
	fr := &fakeRemoteOps{files: map[string][]byte{}, failExecArgs: []string{"status"}}
	snap := modeltest.SampleSnapshot()
	svc, c, _ := confSvc(t, nil, fr, snap)
	svc.Confirmer = &fakeConfirm{confirmed: true}
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)

	res, err := svc.ServiceControl(context.Background(), "stop", "pgpool", "pgp1",
		ControlOpts{Yes: true, Confirm: "stop"})
	if err != nil {
		t.Fatalf("ServiceControl: %v", err)
	}
	if res.Outcome != "success" {
		t.Fatalf("outcome %q", res.Outcome)
	}
	found := false
	for _, x := range fr.execs {
		if strings.Contains(x, "stop pgpool-II") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no svc stop exec, got %v", fr.execs)
	}
}

func TestServiceControlLeaderWarn(t *testing.T) {
	fr := &fakeRemoteOps{files: map[string][]byte{}, failExecArgs: []string{"status"}}
	snap := modeltest.SampleSnapshot()
	snap.Watchdog.LeaderHost = "pgp1"
	svc, c, _ := confSvc(t, nil, fr, snap)
	svc.Confirmer = &fakeConfirm{confirmed: true}
	stop := make(chan struct{})
	defer close(stop)
	go advance(c, stop)

	res, err := svc.ServiceControl(context.Background(), "stop", "pgpool", "pgp1",
		ControlOpts{Yes: true, Confirm: "stop"})
	if err != nil {
		t.Fatalf("ServiceControl: %v", err)
	}
	var warn bool
	for _, ck := range res.Preflight.Checks {
		if ck.Name == "watchdog leader" && ck.Result == "WARN" {
			warn = true
		}
	}
	if !warn {
		t.Fatalf("expected watchdog-leader WARN check: %+v", res.Preflight.Checks)
	}
}

func TestServiceControlWrongRoleNoGo(t *testing.T) {
	fr := &fakeRemoteOps{files: map[string][]byte{}}
	svc, _, _ := confSvc(t, nil, fr, modeltest.SampleSnapshot())
	svc.Confirmer = &fakeConfirm{confirmed: true}

	// pgp1 has only the pgpool role — postgresql must NO-GO
	res, err := svc.ServiceControl(context.Background(), "stop", "postgresql", "pgp1",
		ControlOpts{Yes: true, Confirm: "stop"})
	if err == nil {
		t.Fatalf("expected NO-GO error, got %+v", res)
	}
	if res == nil || res.Preflight == nil || res.Preflight.Verdict != "NO-GO" {
		t.Fatalf("expected NO-GO preflight: %+v", res)
	}
	// preflight may probe ssh, but no service mutation must be exec'd
	for _, x := range fr.execs {
		if strings.Contains(x, "stop") || strings.Contains(x, "start") || strings.Contains(x, "restart") {
			t.Fatalf("mutation exec'd on NO-GO: %v", fr.execs)
		}
	}
}

func TestServiceStatusOutput(t *testing.T) {
	fr := &fakeRemoteOps{files: map[string][]byte{}}
	svc, _, _ := confSvc(t, nil, fr, modeltest.SampleSnapshot())
	out, up, err := svc.ServiceStatus(context.Background(), "pgpool", "pgp1")
	if err != nil {
		t.Fatalf("ServiceStatus: %v", err)
	}
	if !up {
		t.Fatalf("expected active on Exec success, got out=%q", out)
	}
	fr.failExecArgs = []string{"status"}
	out, up, err = svc.ServiceStatus(context.Background(), "pgpool", "pgp1")
	if err != nil {
		t.Fatalf("ServiceStatus down: %v", err)
	}
	if up {
		t.Fatal("expected inactive")
	}
}

func TestServiceControlUnknowns(t *testing.T) {
	svc, _, _ := confSvc(t, nil, &fakeRemoteOps{files: map[string][]byte{}}, modeltest.SampleSnapshot())
	if _, err := svc.ServiceControl(context.Background(), "frobnicate", "pgpool", "pgp1", ControlOpts{}); err == nil {
		t.Fatal("unknown verb accepted")
	}
	if _, err := svc.ServiceControl(context.Background(), "stop", "pgpool", "nohost", ControlOpts{}); err == nil {
		t.Fatal("unknown host accepted")
	}
	if _, err := svc.ServiceControl(context.Background(), "stop", "redis", "pgp1", ControlOpts{}); err == nil {
		t.Fatal("unknown unit accepted")
	}
}
