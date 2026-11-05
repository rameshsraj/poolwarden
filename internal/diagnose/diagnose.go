// Package diagnose is the rule engine (design 01 §12): each rule is a pure
// function over the snapshot, history trends and the previous sample.
package diagnose

import (
	"context"
	"sort"
	"time"

	"github.com/smarsh/poolwarden/internal/config"
	"github.com/smarsh/poolwarden/internal/forecast"
	"github.com/smarsh/poolwarden/internal/history"
	"github.com/smarsh/poolwarden/internal/model"
)

// PathCheck is one config self-check result (filled in from M10).
type PathCheck struct {
	Check, Host, Detail string
	Failed              bool
}

// Input is everything a rule may look at.
type Input struct {
	Snap         *model.Snapshot
	Cfg          *config.Config
	Trends       *forecast.Trends // nil when no history
	Prev         *history.Sample  // previous sample for delta rules; nil ok
	Now          time.Time
	Local        string
	FileSettings map[string]map[string]string // host -> param -> file value; nil until M9
	PathChecks   []PathCheck                  // nil until M10
}

// Rule is one catalogue entry; Fn returns raw hits which the Engine turns
// into Findings.
type Rule struct {
	ID          string
	Severity    string // default; rules may escalate by emitting their own
	Description string
	DocRef      string
	Fn          func(Input) []model.Finding
}

// Engine runs the catalogue; it is long-lived inside the shell so FirstSeen
// stays stable per finding ID.
type Engine struct {
	Rules     []Rule
	FirstSeen map[string]time.Time
}

// NewEngine builds an engine over the full catalogue.
func NewEngine() *Engine {
	return &Engine{Rules: Catalogue, FirstSeen: map[string]time.Time{}}
}

// Run executes every rule, normalises IDs/metadata, keeps FirstSeen stable
// and sorts CRIT → WARN → INFO, then by ID.
func (e *Engine) Run(ctx context.Context, in Input) []model.Finding {
	if e.FirstSeen == nil {
		e.FirstSeen = map[string]time.Time{}
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	var out []model.Finding
	for _, r := range e.Rules {
		select {
		case <-ctx.Done():
			goto done
		default:
		}
		for _, f := range r.Fn(in) {
			if f.Rule == "" {
				f.Rule = r.ID
			}
			if f.ID == "" {
				f.ID = f.Rule + ":" + f.Subject
			}
			if f.Severity == "" {
				f.Severity = r.Severity
			}
			if f.DocRef == "" {
				f.DocRef = r.DocRef
			}
			if f.Cause == "" && len(f.Causes) > 0 {
				f.Cause = f.Causes[0]
			}
			if seen, ok := e.FirstSeen[f.ID]; ok {
				f.FirstSeen = seen
			} else {
				f.FirstSeen = now
				e.FirstSeen[f.ID] = now
			}
			out = append(out, f)
		}
	}
done:
	sevRank := map[string]int{model.SevCrit: 0, model.SevWarn: 1, model.SevInfo: 2}
	sort.Slice(out, func(i, j int) bool {
		if sevRank[out[i].Severity] != sevRank[out[j].Severity] {
			return sevRank[out[i].Severity] < sevRank[out[j].Severity]
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ExplainResult is the `explain <id>` block (02 §3.2).
type ExplainResult struct {
	What          string             `json:"what"`
	Evidence      []string           `json:"evidence"`
	LikelyCauses  []string           `json:"likely_causes"`
	RelatedErrors []model.ErrorEvent `json:"related_errors"`
	Fix           []string           `json:"fix"`
	Reference     string             `json:"reference"`
	Firing        bool               `json:"firing"`
}

// Explain builds the explain block for a finding (or just the rule when it
// is not currently firing).
func Explain(f model.Finding, rule Rule, related []model.ErrorEvent, firing bool) ExplainResult {
	res := ExplainResult{
		What:          rule.Description,
		Evidence:      f.Evidence,
		RelatedErrors: related,
		Fix:           f.NextCommands,
		Reference:     orText(f.DocRef, rule.DocRef),
		Firing:        firing,
	}
	res.LikelyCauses = f.Causes
	if len(res.LikelyCauses) == 0 && f.Cause != "" {
		res.LikelyCauses = []string{f.Cause}
	}
	return res
}

func orText(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
