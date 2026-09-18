package staleness

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

const stalenessScanID = "staleness_scan"

// Checks returns this family's single catalogue row. The cfg parameter is
// unused, matching every other check family in internal/checks: weights
// and thresholds are read from check.Deps.Cfg.Staleness at Run time, so
// the catalogue itself is independent of any particular config value.
func Checks(_ config.Config) []check.Check {
	return []check.Check{stalenessScanCheck()}
}

func stalenessScanCheck() check.Check {
	return check.Check{
		ID:      stalenessScanID,
		Nodes:   check.PiOnly,
		Tier:    check.TierEscalate,
		Cadence: check.Daily,
		Run:     runStalenessScan,
	}
}

// runStalenessScan collects every candidate item, scores it (ADR-016),
// and emits a finding for anything that isn't suppressed: warn for a
// delete candidate, info for a watch-list entry. staleness_candidates and
// staleness_watchlist count every band regardless of severity, so the
// digest can show "0 candidates" rather than nothing at all.
func runStalenessScan(ctx context.Context, d check.Deps) (check.Result, error) {
	items, err := Collect(ctx, d)
	if err != nil {
		return check.Result{}, err
	}

	now := d.Now()
	w := d.Cfg.Staleness
	var findings []check.Finding
	var candidates, watchlist int
	for _, it := range items {
		score := ScoreItem(it, w, now)
		switch Band(score.Total, w) {
		case BandCandidate:
			candidates++
			findings = append(findings, stalenessFinding(d, it, score, check.SeverityWarn))
		case BandWatchlist:
			watchlist++
			findings = append(findings, stalenessFinding(d, it, score, check.SeverityInfo))
		}
	}

	return check.Result{
		Findings: findings,
		Metrics: map[string]float64{
			"staleness_candidates": float64(candidates),
			"staleness_watchlist":  float64(watchlist),
		},
	}, nil
}

// stalenessFinding builds one item's finding: the summary names the two
// components that contributed most to its score, and Data carries the
// full breakdown plus enough context (title, kind, size, last watch,
// requester) for a renderer to describe the item without looking
// anything else up.
func stalenessFinding(d check.Deps, it Item, score Score, sev check.Severity) check.Finding {
	summary := fmt.Sprintf("%s stale (score %d): %s",
		it.Title, int(math.Round(score.Total)), strings.Join(TopComponents(score.Components), ", "))
	f := d.NewFinding(stalenessScanID, it.EntityKey, sev, check.TierEscalate, summary)
	f.Data = map[string]any{
		"score":       score.Total,
		"components":  score.Components,
		"sizeBytes":   it.SizeBytes,
		"lastWatched": it.LastWatched,
		"requestedBy": it.RequestedBy,
		"title":       it.Title,
		"kind":        it.Kind,
	}
	return f
}

// componentOrder fixes the tie-break order TopComponents uses when two
// components share a value, so output is deterministic rather than
// depending on Go's unspecified map iteration order.
var componentOrder = []string{"days", "completion", "arr", "size", "requester", "age"}

// TopComponents names the two highest-valued components (ties broken by
// componentOrder) — the ones that best explain why the item scored high
// enough to surface. Exported so the CLI's `staleness score` table can
// show the same explanation this check's finding summaries do.
func TopComponents(c map[string]float64) []string {
	type kv struct {
		key string
		val float64
	}
	ranked := make([]kv, 0, len(componentOrder))
	for _, k := range componentOrder {
		ranked = append(ranked, kv{k, c[k]})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].val > ranked[j].val })

	n := 2
	if len(ranked) < n {
		n = len(ranked)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = fmt.Sprintf("%s: %.0f", ranked[i].key, ranked[i].val)
	}
	return out
}
