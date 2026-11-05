package pgconf

import (
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	old := []byte("a = 1\nb = 2\nc = 3\nd = 4\ne = 5\nf = 6\ng = 7\n")
	newB := []byte("a = 1\nb = 2\nc = 3\nd = 4\ne = 5\nf = 6\ng = 8\nh = 9\n")
	d := Diff("old.conf", "new.conf", old, newB)
	if !strings.Contains(d, "--- old.conf") || !strings.Contains(d, "+++ new.conf") {
		t.Fatal("headers")
	}
	if !strings.Contains(d, "-g = 7") || !strings.Contains(d, "+g = 8") || !strings.Contains(d, "+h = 9") {
		t.Fatalf("hunk:\n%s", d)
	}
	if !strings.Contains(d, "@@") {
		t.Fatal("no hunk header")
	}
	// no changes → no hunks
	if d2 := Diff("o", "n", old, old); strings.Contains(d2, "@@") {
		t.Errorf("empty diff has hunks: %s", d2)
	}
	// change in middle keeps ≤3 context lines
	mid := []byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10\n")
	mid2 := []byte("l1\nl2\nl3\nl4\nCHANGED\nl6\nl7\nl8\nl9\nl10\n")
	d3 := Diff("o", "n", mid, mid2)
	if strings.Contains(d3, "l1\n") && strings.Contains(d3, "-l1") {
		t.Error("distant context leaked")
	}
	if !strings.Contains(d3, " l4") || !strings.Contains(d3, " l6") {
		t.Errorf("context:\n%s", d3)
	}
}
