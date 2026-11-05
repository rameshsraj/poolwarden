package remote

import "testing"

func TestQuote(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"ls", "/tmp"}, "'ls' '/tmp'"},
		{[]string{"echo", "hello world"}, "'echo' 'hello world'"},
		{[]string{"echo", "it's"}, `'echo' 'it'\''s'`},
		{[]string{"echo", "$HOME"}, `'echo' '$HOME'`},
		{[]string{"echo", "`id`"}, `'echo' '` + "`id`" + `'`},
		{[]string{"echo", "a\nb"}, "'echo' 'a\nb'"},
		{[]string{"echo", ""}, "'echo' ''"},
		{[]string{"one"}, "'one'"},
		{[]string{"a'b'c"}, `'a'\''b'\''c'`},
	}
	for _, tt := range tests {
		if got := Quote(tt.args); got != tt.want {
			t.Errorf("Quote(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}
