package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/smarsh/poolwarden/internal/service"
)

// ttyConfirmer prompts on the real terminal; stdin/stdout come from Deps.
type ttyConfirmer struct {
	in    io.Reader
	out   io.Writer
	isTTY bool
	now   func() time.Time
}

func newConfirmer(a *app) service.Confirmer {
	isTTY := false
	if f, ok := a.deps.Stdin.(*os.File); ok {
		isTTY = term.IsTerminal(int(f.Fd()))
	}
	now := time.Now
	if a.deps.Clock != nil {
		now = a.deps.Clock.Now
	}
	return &ttyConfirmer{in: a.deps.Stdin, out: a.deps.Stdout, isTTY: isTTY, now: now}
}

func (c *ttyConfirmer) AskOperator(ctx context.Context) (string, error) {
	fmt.Fprint(c.out, "Your name/ID: ")
	return c.read(ctx)
}

func (c *ttyConfirmer) AskTicket(ctx context.Context) (string, error) {
	fmt.Fprint(c.out, "Ticket (Enter to skip): ")
	return c.read(ctx)
}

func (c *ttyConfirmer) Confirm(ctx context.Context, p service.Prompt) (service.Decision, error) {
	if p.Plan != "" {
		fmt.Fprintln(c.out, p.Plan)
	}
	fmt.Fprint(c.out, p.Question+" ")
	got, err := c.read(ctx)
	if err != nil {
		return service.Decision{Aborted: true}, nil
	}
	got = strings.TrimSpace(got)
	if p.YesNo {
		return service.Decision{Confirmed: got == "y" || got == "yes", At: c.now()}, nil
	}
	return service.Decision{Confirmed: got == p.Token, Aborted: false, At: c.now()}, nil
}

func (c *ttyConfirmer) read(ctx context.Context) (string, error) {
	ctx2, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ch := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(c.in)
		if sc.Scan() {
			ch <- sc.Text()
			return
		}
		ch <- ""
	}()
	select {
	case <-ctx2.Done():
		return "", ctx2.Err()
	case s := <-ch:
		return s, nil
	}
}

// AskSecret reads a password without echo (x/term).
func (c *ttyConfirmer) AskSecret(ctx context.Context, prompt string) (string, error) {
	f, ok := c.in.(*os.File)
	if !ok || !c.isTTY {
		return "", fmt.Errorf("password prompt needs a TTY")
	}
	fmt.Fprint(c.out, prompt)
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(c.out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
