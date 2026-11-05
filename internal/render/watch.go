package render

import (
	"strings"
	"time"
)

// Views for watch (02 §2.9). View 6 (errors) is name-only.
var WatchViews = map[string]int{
	"status": 1, "nodes": 2, "lag": 3, "pools": 4, "watchdog": 5, "errors": 6,
}

// KeyAction maps a key press to a watch action.
type KeyAction struct {
	Kind string // quit|pause|interval|view
	Arg  float64
}

// MapKey implements the 02 §2.9 key table.
func MapKey(k byte, view int) *KeyAction {
	switch k {
	case 'q':
		return &KeyAction{Kind: "quit"}
	case 'p':
		return &KeyAction{Kind: "pause"}
	case '+', '=':
		return &KeyAction{Kind: "interval", Arg: 0.5}
	case '-', '_':
		return &KeyAction{Kind: "interval", Arg: 2}
	case '1', '2', '3', '4', '5':
		return &KeyAction{Kind: "view", Arg: float64(k - '0')}
	}
	return nil
}

// ClampInterval halves/doubles within [1s, 60s].
func ClampInterval(d time.Duration, factor float64) time.Duration {
	nd := time.Duration(float64(d) * factor)
	if nd < time.Second {
		nd = time.Second
	}
	if nd > 60*time.Second {
		nd = 60 * time.Second
	}
	return nd
}

const (
	altScreenOn  = "\x1b[?1049h"
	altScreenOff = "\x1b[?1049l"
	cursorHide   = "\x1b[?25l"
	cursorShow   = "\x1b[?25h"
	clearHome    = "\x1b[H\x1b[2J"
)

// Frame builds one watch frame: status line + view output.
func Frame(view string, every time.Duration, paused bool, at time.Time, body string) string {
	var b strings.Builder
	b.WriteString(clearHome)
	status := "watch " + view + "  every " + every.String() + "  " +
		at.Format("15:04:05") + "  [q quit  p pause  +/- interval  1-5 view]"
	if paused {
		status += "  PAUSED"
	}
	b.WriteString(status + "\n\n")
	b.WriteString(body)
	return b.String()
}

// TermCtl returns the enter/leave escape sequences.
func TermCtl() (enter, leave string) {
	return altScreenOn + cursorHide, altScreenOff + cursorShow
}
