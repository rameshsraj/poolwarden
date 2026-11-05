package pgconf

import (
	"fmt"
	"strings"
)

// Diff produces a unified diff (LCS-based, 3 context lines).
func Diff(oldName, newName string, old, new []byte) string {
	a := strings.Split(strings.TrimSuffix(string(old), "\n"), "\n")
	b := strings.Split(strings.TrimSuffix(string(new), "\n"), "\n")
	if len(a) == 1 && a[0] == "" {
		a = nil
	}
	if len(b) == 1 && b[0] == "" {
		b = nil
	}
	ops := lcsOps(a, b)
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)
	// group ops into hunks with 3 context lines
	for i := 0; i < len(ops); {
		if ops[i].kind == " " {
			// find next change
			j := i
			for j < len(ops) && ops[j].kind == " " {
				j++
			}
			i = j
			continue
		}
		// hunk start: back up ≤3 context
		start := i
		ctxBack := 0
		for start > 0 && ctxBack < 3 && ops[start-1].kind == " " {
			start--
			ctxBack++
		}
		// extend end while changes within 2*3 context
		end := i
		lastChange := i
		for end < len(ops) {
			if ops[end].kind != " " {
				lastChange = end
				end++
			} else {
				end++
				if end-lastChange > 6 {
					end = lastChange + 3
					break
				}
			}
		}
		if end > len(ops) {
			end = len(ops)
		}
		// trim trailing context >3
		trail := 0
		for end-1 > lastChange && end-1 >= 0 && ops[end-1].kind == " " && trail < end-lastChange-3 {
			end--
			trail++
		}
		// emit hunk
		var oldC, newC int
		for k := 0; k < start; k++ {
			if ops[k].kind != "-" {
				oldC++
			}
			if ops[k].kind != "+" {
				newC++
			}
		}
		o0, n0 := oldC+1, newC+1
		ol, nl := 0, 0
		for k := start; k < end; k++ {
			if ops[k].kind != "-" {
				ol++
			}
			if ops[k].kind != "+" {
				nl++
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", o0, ol, n0, nl)
		for k := start; k < end; k++ {
			out.WriteString(ops[k].kind + ops[k].s + "\n")
		}
		i = end
	}
	return out.String()
}

func lcsOps(a, b []string) []struct{ kind, s string } {
	// LCS via DP (configs are small)
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []struct{ kind, s string }
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, struct{ kind, s string }{" ", a[i]})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, struct{ kind, s string }{"-", a[i]})
			i++
		} else {
			ops = append(ops, struct{ kind, s string }{"+", b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, struct{ kind, s string }{"-", a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, struct{ kind, s string }{"+", b[j]})
	}
	return ops
}
