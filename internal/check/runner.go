package check

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Run executes checks sequentially against d, giving each check its own
// context with timeout. A check error never aborts the run: it is recorded
// in Report.Errors. ErrNotConfigured is recorded in Report.Skipped.
// Findings are sorted by (Severity desc, CheckID, EntityKey) for stable
// output; Metrics from every check are merged (later checks win on
// duplicate keys, which the all.go test forbids).
func Run(ctx context.Context, checks []Check, d Deps, timeout time.Duration) Report {
	now := d.Now()
	rep := Report{Node: d.Node, GeneratedAt: now, Findings: []Finding{}, Ran: []string{}, Skipped: []string{}, Errors: []CheckError{}, Metrics: map[string]float64{}}
	for _, c := range checks {
		res, err := runOne(ctx, c, d, timeout)
		switch {
		case errors.Is(err, ErrNotConfigured):
			rep.Skipped = append(rep.Skipped, c.ID)
		case err != nil:
			rep.Errors = append(rep.Errors, CheckError{CheckID: c.ID, Error: err.Error()})
		default:
			rep.Ran = append(rep.Ran, c.ID)
			rep.Findings = append(rep.Findings, res.Findings...)
			for k, v := range res.Metrics {
				rep.Metrics[k] = v
			}
		}
	}
	sortFindings(rep.Findings)
	rep.ChecksRun = len(rep.Ran) + len(rep.Errors)
	rep.ChecksFailed = len(rep.Errors)
	return rep
}

// runOne applies the timeout and converts a panic inside a check into an
// error so one bad check cannot take the daemon down.
func runOne(ctx context.Context, c Check, d Deps, timeout time.Duration) (res Result, err error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("check %s panicked: %v", c.ID, r)
		}
	}()
	res, err = c.Run(cctx, d)
	if err != nil && !errors.Is(err, ErrNotConfigured) {
		return Result{}, fmt.Errorf("check %s: %w", c.ID, err)
	}
	return res, err
}

// sortFindings orders findings by (Severity desc, CheckID, EntityKey) so
// runs produce stable, deterministic output.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if a.CheckID != b.CheckID {
			return a.CheckID < b.CheckID
		}
		return a.EntityKey < b.EntityKey
	})
}

func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityWarn:
		return 1
	default:
		return 2
	}
}
