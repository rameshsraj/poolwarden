package model

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseLSN parses a WAL location like "3/A1B2C3D4" into bytes.
func ParseLSN(s string) (uint64, error) {
	hi, lo, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || hi == "" || lo == "" {
		return 0, fmt.Errorf("invalid LSN %q", s)
	}
	h, err := strconv.ParseUint(hi, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid LSN %q: %w", s, err)
	}
	l, err := strconv.ParseUint(lo, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid LSN %q: %w", s, err)
	}
	return h<<32 | l, nil
}

// LSNDiff returns a-b in bytes, or -1 when either side is unparseable.
func LSNDiff(a, b string) int64 {
	av, err1 := ParseLSN(a)
	bv, err2 := ParseLSN(b)
	if err1 != nil || err2 != nil {
		return -1
	}
	return int64(av) - int64(bv)
}
