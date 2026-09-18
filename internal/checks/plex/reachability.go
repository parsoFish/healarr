package plex

import (
	"context"
	"fmt"

	"github.com/parsoFish/healarr/internal/check"
)

const plexReachabilityID = "plex_reachability"

// plexEntityKey is the entity key for Plex's service-level reachability
// condition.
const plexEntityKey = "plex"

// plexReachabilityCheck watches that Plex answers /identity, and records
// reachability as a metric for the digest to trend over time.
func plexReachabilityCheck() check.Check {
	return check.Check{
		ID:      plexReachabilityID,
		Nodes:   check.NASOnly,
		Tier:    check.TierObserve,
		Cadence: check.Every5m,
		Run:     runPlexReachability,
	}
}

// runPlexReachability turns an unreachable Plex into a critical finding
// rather than a check error (reachability is this check's purpose, so the
// error never propagates as a check error). A reachable Plex produces no
// finding, only the plex_reachable metric.
func runPlexReachability(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.Plex == nil {
		return check.Result{}, check.ErrNotConfigured
	}

	if _, err := d.Plex.Identity(ctx); err != nil {
		f := d.NewFinding(plexReachabilityID, plexEntityKey, check.SeverityCritical, check.TierObserve,
			fmt.Sprintf("plex unreachable: %s", err.Error()))
		return check.Result{Findings: []check.Finding{f}}, nil
	}

	return check.Result{Metrics: map[string]float64{"plex_reachable": 1}}, nil
}
