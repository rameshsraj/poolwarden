//go:build unix

package audit

import "log/syslog"

// Syslog writes entries to authpriv.notice.
type Syslog struct{}

func (Syslog) Write(priority, msg string) error {
	w, err := syslog.New(syslog.LOG_AUTHPRIV|syslog.LOG_NOTICE, "poolwarden")
	if err != nil {
		return err
	}
	defer w.Close()
	return w.Notice(msg)
}
