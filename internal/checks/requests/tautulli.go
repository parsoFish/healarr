package requests

import (
	"context"
	"fmt"

	"github.com/parsoFish/healarr/internal/check"
)

const tautulliReachabilityID = "tautulli_reachability"

// tautulliEntityKey is the entity key for Tautulli's service-level
// reachability condition.
const tautulliEntityKey = "tautulli"

// tautulliReachabilityCheck watches that Tautulli answers pings, and
// records its current active-stream count as a metric.
func tautulliReachabilityCheck() check.Check {
	return check.Check{
		ID:      tautulliReachabilityID,
		Nodes:   check.PiOnly,
		Tier:    check.TierObserve,
		Cadence: check.Daily,
		Run:     runTautulliReachability,
	}
}

// runTautulliReachability turns an unreachable Tautulli into a warn
// finding rather than a check error (reachability is this check's
// purpose). When Tautulli answers, it records the active-stream count as
// a metric instead; a failure fetching that count is a genuine check
// error since reachability has already been confirmed.
func runTautulliReachability(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.Tautulli == nil {
		return check.Result{}, check.ErrNotConfigured
	}

	if err := d.Tautulli.Ping(ctx); err != nil {
		f := d.NewFinding(tautulliReachabilityID, tautulliEntityKey, check.SeverityWarn, check.TierObserve,
			fmt.Sprintf("tautulli unreachable: %s", err.Error()))
		return check.Result{Findings: []check.Finding{f}}, nil
	}

	streams, err := d.Tautulli.ActivityCount(ctx)
	if err != nil {
		return check.Result{}, fmt.Errorf("activity count: %w", err)
	}
	return check.Result{Metrics: map[string]float64{"tautulli_active_streams": float64(streams)}}, nil
}
