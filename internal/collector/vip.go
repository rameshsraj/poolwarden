package collector

import (
	"context"
	"strings"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/model"
)

// vipAddress finds the delegate IP: watchdog's DelegateIP first, then the
// delegate_ip setting.
func vipAddress(s *model.Snapshot) string {
	for _, n := range s.Watchdog.Nodes {
		if n.DelegateIP != "" {
			return n.DelegateIP
		}
	}
	return s.Settings["delegate_ip"]
}

// checkVIP runs `ip -o addr show` on one pgpool host and looks for ip among
// its inet addresses.
func (c *Collector) checkVIP(ctx context.Context, h config.Host, ip string) model.VIPCheck {
	vc := model.VIPCheck{Host: h.Name}
	if ip == "" {
		vc.Err = "no delegate_ip configured"
		return vc
	}
	if c.Remote == nil {
		vc.Err = "no remote access"
		return vc
	}
	sess, err := c.Remote.Session(ctx, h)
	if err != nil {
		vc.Err = err.Error()
		return vc
	}
	res, err := sess.Exec(ctx, []string{c.Cfg.Bins.IP, "-o", "addr", "show"}, nil)
	if err != nil {
		vc.Err = err.Error()
		return vc
	}
	if res.Exit != 0 {
		vc.Err = "ip addr show exited " + string(res.Stderr)
		return vc
	}
	present, ifaces := ParseIPAddrShow(string(res.Stdout), ip)
	vc.Present = present
	vc.Interfaces = ifaces
	return vc
}

// ParseIPAddrShow reports whether ip appears as an inet address in `ip -o
// addr show` output, and which interfaces carry it.
func ParseIPAddrShow(out, ip string) (bool, []string) {
	var ifaces []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// e.g. "2: eth0    inet 10.0.0.100/24 brd ..."
		for i, f := range fields {
			if f != "inet" || i+1 >= len(fields) {
				continue
			}
			addr := fields[i+1]
			if slash := strings.IndexByte(addr, '/'); slash >= 0 {
				addr = addr[:slash]
			}
			if addr == ip && len(fields) > 1 {
				ifaces = append(ifaces, strings.TrimSuffix(fields[1], ":"))
			}
		}
	}
	return len(ifaces) > 0, ifaces
}
