package requests

import (
	"context"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
)

const overseerrStuckProcessingID = "overseerr_stuck_processing"

// overseerrProcessingFilter is the Overseerr request filter value for
// requests currently being worked (as opposed to pending/approved/
// available).
const overseerrProcessingFilter = "processing"

// overseerrStuckProcessingCheck watches Overseerr's "processing" requests
// for ones that have sat there longer than the configured threshold,
// suggesting Overseerr never heard back from Sonarr/Radarr or the
// download client, and records how many requests are currently
// processing.
func overseerrStuckProcessingCheck() check.Check {
	return check.Check{
		ID:      overseerrStuckProcessingID,
		Nodes:   check.PiOnly,
		Tier:    check.TierObserve,
		Cadence: check.Daily,
		Run:     runOverseerrStuckProcessing,
	}
}

func runOverseerrStuckProcessing(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.Overseerr == nil {
		return check.Result{}, check.ErrNotConfigured
	}

	reqs, err := d.Overseerr.Requests(ctx, overseerrProcessingFilter)
	if err != nil {
		return check.Result{}, fmt.Errorf("requests: %w", err)
	}

	var findings []check.Finding
	now := d.Now()
	for _, r := range reqs {
		if r.UpdatedAt.IsZero() || now.Sub(r.UpdatedAt) <= d.Cfg.Checks.OverseerrStuckAfter {
			continue
		}
		findings = append(findings, stuckProcessingFinding(d, r))
	}

	return check.Result{
		Findings: findings,
		Metrics:  map[string]float64{"overseerr_processing": float64(len(reqs))},
	}, nil
}

// stuckProcessingFinding builds the warn finding for one request that has
// been processing longer than the configured threshold. The timestamp is
// rendered as RFC3339, matching the rest of this check family (e.g.
// indexer_failures).
func stuckProcessingFinding(d check.Deps, r overseerr.Request) check.Finding {
	key := fmt.Sprintf("overseerr:%d", r.ID)
	summary := fmt.Sprintf("overseerr request #%d (%s tmdb:%d) processing since %s for %s",
		r.ID, r.MediaType, r.TMDBID, r.UpdatedAt.Format(time.RFC3339), r.RequestedBy)
	return d.NewFinding(overseerrStuckProcessingID, key, check.SeverityWarn, check.TierObserve, summary)
}
