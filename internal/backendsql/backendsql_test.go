package backendsql

import (
	"testing"
)

func TestMapReplicationRow(t *testing.T) {
	lag := float64(0.3)
	r := MapReplicationRow("db2", "10.0.0.12", "streaming", "async",
		"3/A1B2C3D4", "3/A1B2C000", "3/A1B2B000", "3/A1B2A000",
		1024, &lag, nil, &lag)
	if r.AppName != "db2" || r.LagBytes != 1024 || r.WriteLag != 0.3 ||
		r.FlushLag != -1 || r.ReplayLag != 0.3 {
		t.Errorf("%+v", r)
	}
}

func TestMapSlotRow(t *testing.T) {
	s := MapSlotRow("s1", "physical", true, "reserved", 4096, "postgres")
	if s.Name != "s1" || !s.Active || s.RetainedBytes != 4096 || s.Database != "postgres" {
		t.Errorf("%+v", s)
	}
}

func TestQueriesAreReadOnly(t *testing.T) {
	for name, q := range map[string]string{
		"QSelf": QSelf, "QPrimaryLSN": QPrimaryLSN, "QStandbyLSN": QStandbyLSN,
		"QWalReceiver": QWalReceiver, "QReplication": QReplication,
		"QSlots": QSlots, "QLongTxns": QLongTxns, "QConns": QConns,
		"QConflicts": QConflicts,
	} {
		if q[:6] != "SELECT" {
			t.Errorf("%s is not a SELECT", name)
		}
	}
}
