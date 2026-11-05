//go:build unix

package audit

import "os"

func getenv(k string) string    { return os.Getenv(k) }
func hostname() (string, error) { return os.Hostname() }
func pid() int                  { return os.Getpid() }
func ttyName() string {
	if f, err := os.Readlink("/proc/self/fd/0"); err == nil {
		return f
	}
	return ""
}
