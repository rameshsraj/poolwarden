package logsrc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/smarsh/poolwarden/internal/remote"
)

// ReadOpts filters a Read.
type ReadOpts struct {
	Since    time.Duration
	Grep     *regexp.Regexp
	MaxLines int
	Now      time.Time // zero = time.Now
}

// Reader is one log source.
type Reader interface {
	Read(ctx context.Context, opts ReadOpts) ([]Line, error)
	Follow(ctx context.Context, opts ReadOpts, out chan<- Line) error
	Context(ctx context.Context, file string, lineNo int64, n int) ([]string, error)
}

// FileSource reads a component log over a remote.Session.
type FileSource struct {
	Host      string
	Component string
	Dir       string
	Filename  string // may contain strftime escapes; empty → newest file
	Prefix    *Prefix
	Session   remote.Session
}

func expandStrftime(pat string, t time.Time) string {
	var b strings.Builder
	for i := 0; i < len(pat); i++ {
		if pat[i] == '%' && i+1 < len(pat) {
			i++
			switch pat[i] {
			case 'Y':
				fmt.Fprintf(&b, "%04d", t.Year())
			case 'm':
				fmt.Fprintf(&b, "%02d", int(t.Month()))
			case 'd':
				fmt.Fprintf(&b, "%02d", t.Day())
			case 'H':
				fmt.Fprintf(&b, "%02d", t.Hour())
			case 'M':
				fmt.Fprintf(&b, "%02d", t.Minute())
			case 'S':
				fmt.Fprintf(&b, "%02d", t.Second())
			case 'a':
				b.WriteString(t.Format("Mon"))
			case 'A':
				b.WriteString(t.Format("Monday"))
			case 'b', 'h':
				b.WriteString(t.Format("Jan"))
			case 'B':
				b.WriteString(t.Format("January"))
			case 'j':
				fmt.Fprintf(&b, "%03d", t.YearDay())
			case 'u':
				fmt.Fprintf(&b, "%d", (int(t.Weekday())+6)%7+1)
			case 'w':
				fmt.Fprintf(&b, "%d", int(t.Weekday()))
			case '%':
				b.WriteByte('%')
			default:
				b.WriteByte('%')
				b.WriteByte(pat[i])
			}
		} else {
			b.WriteByte(pat[i])
		}
	}
	return b.String()
}

func (f *FileSource) list(ctx context.Context) ([]string, error) {
	res, err := f.Session.Exec(ctx, []string{"ls", "-1t", "--", f.Dir}, nil)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

func (f *FileSource) files(ctx context.Context, now time.Time) ([]string, error) {
	listed, err := f.list(ctx)
	if err != nil || len(listed) == 0 {
		return listed, err
	}
	if f.Filename == "" {
		return listed[:1], nil
	}
	today := expandStrftime(f.Filename, now)
	yday := expandStrftime(f.Filename, now.Add(-24*time.Hour))
	var out []string
	for _, lf := range listed {
		if lf == today || lf == yday || strings.HasPrefix(lf, strings.Split(today, ".")[0]) {
			out = append(out, lf)
		}
	}
	if len(out) == 0 {
		out = listed[:1]
	}
	sort.Strings(out)
	return out, nil
}

// Read collects parsed lines from the window.
func (f *FileSource) Read(ctx context.Context, opts ReadOpts) ([]Line, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	files, err := f.files(ctx, now)
	if err != nil {
		return nil, err
	}
	var all []Line
	var lineNo int64
	cutoff := now.Add(-opts.Since)
	for _, file := range files {
		path := f.Dir + "/" + file
		n := int64(200000)
		if opts.MaxLines > 0 {
			n = int64(opts.MaxLines)
		}
		res, err := f.Session.Exec(ctx, []string{"tail", "-n", fmt.Sprint(n), "--", path}, nil)
		if err != nil {
			return all, err
		}
		for _, raw := range strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n") {
			lineNo++
			l, ok := f.parseLine(raw)
			if !ok {
				if IsContinuation(raw) && len(all) > 0 {
					all[len(all)-1].Continuation = append(all[len(all)-1].Continuation, raw)
				}
				continue
			}
			l.Host, l.Component, l.File, l.LineNo = f.Host, f.Component, path, lineNo
			if !l.TS.IsZero() && l.TS.Before(cutoff) {
				continue
			}
			if opts.Grep != nil && !opts.Grep.MatchString(raw) {
				continue
			}
			all = append(all, l)
		}
	}
	if opts.MaxLines > 0 && len(all) > opts.MaxLines {
		all = all[len(all)-opts.MaxLines:]
	}
	return all, nil
}

func (f *FileSource) parseLine(raw string) (Line, bool) {
	if strings.HasPrefix(raw, "# SYNTHETIC") {
		return Line{}, false
	}
	return f.Prefix.Parse(raw)
}

// Follow streams `tail -n 0 -F` until ctx is done; the child is killed on
// cancel and the function returns only after it exits.
func (f *FileSource) Follow(ctx context.Context, opts ReadOpts, out chan<- Line) error {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	files, err := f.files(ctx, now)
	if err != nil || len(files) == 0 {
		return err
	}
	path := f.Dir + "/" + files[len(files)-1]
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- f.Session.ExecStream(ctx, []string{"tail", "-n", "0", "-F", "--", path}, pw)
		pw.Close()
	}()
	lines := make(chan string, 64)
	go func() {
		r := bufio.NewReader(pr)
		for {
			raw, err := r.ReadString('\n')
			if raw != "" {
				select {
				case lines <- strings.TrimRight(raw, "\n"):
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			<-done // wait for the tail process to die
			return ctx.Err()
		case err := <-done:
			return err
		case raw := <-lines:
			if l, ok := f.parseLine(raw); ok {
				l.Host, l.Component, l.File = f.Host, f.Component, path
				select {
				case out <- l:
				case <-ctx.Done():
					<-done
					return ctx.Err()
				}
			}
		}
	}
}

// Context returns n lines around lineNo in file.
func (f *FileSource) Context(ctx context.Context, file string, lineNo int64, n int) ([]string, error) {
	start := lineNo - int64(n)
	if start < 1 {
		start = 1
	}
	end := lineNo + int64(n)
	res, err := f.Session.Exec(ctx, []string{"sed", "-n",
		fmt.Sprintf("%d,%dp", start, end), "--", file}, nil)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n"), nil
}
