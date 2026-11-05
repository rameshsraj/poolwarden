package remote

import (
	"context"
	"sync"

	"github.com/smarsh/poolwarden/internal/config"
)

// Pool hands out one Session per inventory host, choosing LocalSession when
// the host is the local machine and the Dialer otherwise. Steps records
// every connect.
type Pool struct {
	Dialer Dialer
	SSHCfg config.SSH
	Local  Session

	mu    sync.Mutex
	sess  map[string]Session
	Steps []*Step
}

func NewPool(d Dialer, cfg config.SSH) *Pool {
	return &Pool{Dialer: d, SSHCfg: cfg, Local: &LocalSession{}, sess: map[string]Session{}}
}

// Session returns the session for h, dialling once and caching.
func (p *Pool) Session(ctx context.Context, h config.Host) (Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sess[h.Name]; ok {
		return s, nil
	}
	if IsLocal(h) {
		p.sess[h.Name] = p.Local
		return p.Local, nil
	}
	s, step, err := p.Dialer.Dial(ctx, h, p.SSHCfg)
	if step != nil {
		p.Steps = append(p.Steps, step)
	}
	if err != nil {
		return nil, err
	}
	p.sess[h.Name] = s
	return s, nil
}

// Close releases every cached session.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.sess {
		s.Close()
	}
	p.sess = map[string]Session{}
}
