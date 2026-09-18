package arr

import (
	"context"
	"fmt"

	"github.com/parsoFish/healarr/internal/check"
)

const arrWantedMissingSpikeID = "arr_wanted_missing_spike"

// arrWantedMissingSpikeCheck watches each app's wanted/missing count for a
// sudden jump — a sign that imports have stopped working, not just that
// the library grew.
func arrWantedMissingSpikeCheck() check.Check {
	return check.Check{
		ID:      arrWantedMissingSpikeID,
		Nodes:   check.PiOnly,
		Tier:    check.TierObserve,
		Cadence: check.Daily,
		Run:     runArrWantedMissingSpike,
	}
}

func runArrWantedMissingSpike(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.Sonarr == nil && d.Radarr == nil {
		return check.Result{}, check.ErrNotConfigured
	}
	var findings []check.Finding
	metrics := map[string]float64{}

	if d.Sonarr != nil {
		f, err := wantedMissingSpike(ctx, d, "sonarr", d.Sonarr.WantedMissingCount, metrics)
		if err != nil {
			return check.Result{}, err
		}
		if f != nil {
			findings = append(findings, *f)
		}
	}
	if d.Radarr != nil {
		f, err := wantedMissingSpike(ctx, d, "radarr", d.Radarr.WantedMissingCount, metrics)
		if err != nil {
			return check.Result{}, err
		}
		if f != nil {
			findings = append(findings, *f)
		}
	}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}

// wantedMissingSpike fetches app's current wanted/missing count, always
// records it as a metric, and — when a previous run's metric exists —
// evaluates the spike rule against it. count is the app's
// WantedMissingCount method, passed in so this stays app-agnostic.
func wantedMissingSpike(ctx context.Context, d check.Deps, app string, count func(context.Context) (int, error), metrics map[string]float64) (*check.Finding, error) {
	n, err := count(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s wanted missing: %w", app, err)
	}
	metricName := app + "_wanted_missing"
	metrics[metricName] = float64(n)

	prev, ok := d.PreviousMetric(metricName)
	if !ok {
		return nil, nil
	}
	return spikeFinding(d, app, prev, n), nil
}

// spikeFinding applies the spike rule: the absolute jump must clear
// WantedSpikeMin, and either there was no previous baseline to take a
// percentage of, or the percentage jump also clears WantedSpikePercent.
func spikeFinding(d check.Deps, app string, prev float64, n int) *check.Finding {
	diff := float64(n) - prev
	if diff < float64(d.Cfg.Checks.WantedSpikeMin) {
		return nil
	}
	if prev != 0 && diff/prev*100 < d.Cfg.Checks.WantedSpikePercent {
		return nil
	}
	f := d.NewFinding(arrWantedMissingSpikeID, app+":wanted", check.SeverityWarn, check.TierObserve,
		fmt.Sprintf("%s: wanted/missing jumped from %.0f to %d", app, prev, n))
	f.Data = map[string]any{"previous": prev, "current": n}
	return &f
}
