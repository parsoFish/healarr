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

// outcome is what one check goroutine reports back to runOne.
type outcome struct {
	res Result
	err error
}

// runOne applies the timeout and converts a panic inside a check into an
// error so one bad check cannot take the daemon down.
//
// The check runs in its own goroutine because a context deadline alone
// cannot bound it: the checks that matter most here end in a syscall
// (stat, statfs, readdir) against an NFS mount, and a syscall stuck on a
// hung mount is uninterruptible — no amount of ctx cancellation returns
// control. So runOne races the result against the deadline and, on
// timeout, deliberately abandons the goroutine rather than waiting for a
// call that may never return. The leak is bounded: at most one goroutine
// per stuck check, and each one exits as soon as its syscall unblocks.
// The result channel is buffered so that abandoned send never blocks.
//
// A check's own error is returned verbatim: CheckError.CheckID already
// carries the id and every renderer prefixes it, so wrapping it with the
// id here would print it twice.
func runOne(ctx context.Context, c Check, d Deps, timeout time.Duration) (Result, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{err: fmt.Errorf("panicked: %v", r)}
			}
		}()
		res, err := c.Run(cctx, d)
		done <- outcome{res: res, err: err}
	}()

	select {
	case o := <-done:
		if o.err != nil && !errors.Is(o.err, ErrNotConfigured) {
			return Result{}, o.err
		}
		return o.res, o.err
	case <-cctx.Done():
		return Result{}, fmt.Errorf("check %s: %w", c.ID, cctx.Err())
	}
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
