package indexers

import (
	"context"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/prowlarr"
)

const indexerFailuresID = "indexer_failures"

// indexerFailuresCheck watches Prowlarr's indexers for ones Prowlarr has
// disabled after repeated failures, or that failed recently but are still
// enabled, and records how many indexers are currently enabled.
func indexerFailuresCheck() check.Check {
	return check.Check{
		ID:      indexerFailuresID,
		Nodes:   check.PiOnly,
		Tier:    check.TierObserve,
		Cadence: check.Every15m,
		Run:     runIndexerFailures,
	}
}

func runIndexerFailures(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.Prowlarr == nil {
		return check.Result{}, check.ErrNotConfigured
	}

	idx, err := d.Prowlarr.Indexers(ctx)
	if err != nil {
		return check.Result{}, fmt.Errorf("indexers: %w", err)
	}
	statuses, err := d.Prowlarr.IndexerStatus(ctx)
	if err != nil {
		return check.Result{}, fmt.Errorf("indexer status: %w", err)
	}

	names := indexerNames(idx)
	var findings []check.Finding
	for _, s := range statuses {
		if f := indexerStatusFinding(d, names, s); f != nil {
			findings = append(findings, *f)
		}
	}

	return check.Result{
		Findings: findings,
		Metrics:  map[string]float64{"prowlarr_indexers_enabled": float64(countEnabled(idx))},
	}, nil
}

// indexerNames maps each indexer's ID to its name, for statuses to look up
// against.
func indexerNames(idx []prowlarr.Indexer) map[int64]string {
	names := make(map[int64]string, len(idx))
	for _, ix := range idx {
		names[ix.ID] = ix.Name
	}
	return names
}

// indexerName resolves id against names, falling back to "#<id>" for a
// status whose indexer Prowlarr no longer (or does not yet) list.
func indexerName(names map[int64]string, id int64) string {
	if name, ok := names[id]; ok {
		return name
	}
	return fmt.Sprintf("#%d", id)
}

// countEnabled counts indexers with Enable set.
func countEnabled(idx []prowlarr.Indexer) int {
	n := 0
	for _, ix := range idx {
		if ix.Enable {
			n++
		}
	}
	return n
}

// indexerStatusFinding applies the indexer_failures rule: an indexer
// Prowlarr has disabled until a future time is a warn; failing that, one
// that failed within the configured window is an info. Anything else
// (healthy, or a failure old enough to have fallen outside the window)
// produces no finding.
func indexerStatusFinding(d check.Deps, names map[int64]string, s prowlarr.IndexerStatus) *check.Finding {
	name := indexerName(names, s.IndexerID)
	key := fmt.Sprintf("prowlarr:%d", s.IndexerID)
	now := d.Now()

	if disabled := !s.DisabledTill.IsZero() && s.DisabledTill.After(now); disabled {
		f := d.NewFinding(indexerFailuresID, key, check.SeverityWarn, check.TierObserve,
			fmt.Sprintf("indexer %s disabled until %s", name, s.DisabledTill.Format(time.RFC3339)))
		return &f
	}

	recent := !s.MostRecentFailure.IsZero() && now.Sub(s.MostRecentFailure) <= d.Cfg.Checks.IndexerFailureWindow
	if recent {
		f := d.NewFinding(indexerFailuresID, key, check.SeverityInfo, check.TierObserve,
			fmt.Sprintf("indexer %s failed at %s", name, s.MostRecentFailure.Format(time.RFC3339)))
		return &f
	}
	return nil
}
