// Package pcptest provides a scripted pcp.Runner for tests.
package pcptest

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// Call records one Run invocation.
type Call struct {
	Bin  string
	Args []string
}

// FakeRunner returns scripted output keyed by "bin arg arg...".
type FakeRunner struct {
	LastStdin []byte
	mu        sync.Mutex
	Calls     []Call
	// Responses maps "<basename> <args joined by space>" to output.
	// "*" is a catch-all.
	Responses map[string]Resp
	// Err, when set, fails every call (e.g. context ignored).
	Err error
}

// Resp is one scripted result.
type Resp struct {
	Stdout string
	Stderr string
	Exit   int
	Err    error
	// Block makes the call wait for ctx cancellation, then fail.
	Block bool
}

func New() *FakeRunner { return &FakeRunner{Responses: map[string]Resp{}} }

// On scripts a response for an exact argv suffix match.
func (f *FakeRunner) On(match string, r Resp) { f.Responses[match] = r }

// LastStdin records the stdin given to RunInput.
func (f *FakeRunner) RunInput(ctx context.Context, bin string, stdin []byte, args ...string) ([]byte, []byte, int, error) {
	f.LastStdin = stdin
	return f.Run(ctx, bin, args...)
}

func (f *FakeRunner) Run(ctx context.Context, bin string, args ...string) ([]byte, []byte, int, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, Call{Bin: bin, Args: append([]string(nil), args...)})
	key := filepath.Base(bin) + " " + strings.Join(args, " ")
	r, ok := f.Responses[key]
	if !ok {
		for k, v := range f.Responses {
			if k == "*" {
				continue
			}
			pre, suf := strings.HasPrefix(k, "*"), strings.HasSuffix(k, "*")
			switch {
			case pre && suf && strings.Contains(key, k[1:len(k)-1]),
				suf && strings.HasPrefix(key, k[:len(k)-1]),
				pre && strings.HasSuffix(key, k[1:]):
				r, ok = v, true
			}
			if ok {
				break
			}
		}
	}
	if !ok {
		r, ok = f.Responses["*"]
	}
	err := f.Err
	f.mu.Unlock()
	if r.Block {
		<-ctx.Done()
		return nil, nil, -1, ctx.Err()
	}
	if err != nil {
		return nil, nil, -1, err
	}
	if r.Err != nil {
		return nil, nil, -1, r.Err
	}
	if !ok && r.Exit == 0 && r.Stdout == "" && r.Stderr == "" {
		return nil, []byte(fmt.Sprintf("no scripted response for %q", key)), 127, nil
	}
	return []byte(r.Stdout), []byte(r.Stderr), r.Exit, nil
}
