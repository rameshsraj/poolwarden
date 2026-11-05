package collector

import (
	"os"
	"testing"
)

func TestParseIPAddrShow(t *testing.T) {
	b, err := os.ReadFile("testdata/ip_addr_show.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	present, ifaces := ParseIPAddrShow(out, "10.0.0.100")
	if !present || len(ifaces) != 1 || ifaces[0] != "eth0" {
		t.Errorf("%v %v", present, ifaces)
	}
	present, _ = ParseIPAddrShow(out, "10.0.0.99")
	if present {
		t.Error("10.0.0.99 should not be present")
	}
	present, ifaces = ParseIPAddrShow(out, "10.0.0.11")
	if !present || ifaces[0] != "eth0" {
		t.Errorf("%v %v", present, ifaces)
	}
}
