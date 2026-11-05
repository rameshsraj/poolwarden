package remote_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Hard constraint: no shells — no non-test .go file may exec sh/bash.
func TestNoShellExec(t *testing.T) {
	bad := []string{`"sh", "-c"`, `"bash"`, `"/bin/sh"`, `execRaw(`}
	err := filepath.Walk("../..", func(p string, i os.FileInfo, err error) error {
		if err != nil || i.IsDir() || !strings.HasSuffix(p, ".go") ||
			strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, pat := range bad {
			if strings.Contains(string(b), pat) {
				t.Errorf("%s contains %s", p, pat)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
