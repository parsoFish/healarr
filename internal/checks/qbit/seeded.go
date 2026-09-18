package qbit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
)

const seededDoneID = "seeded_done"

// seededDoneCheck flags torrents that have already met qBittorrent's own
// seeding target (ratio or time, from its preferences), have sat seeded
// for at least SeededMinAge, and have a confirmed arr import: there is
// nothing left for them to do, so they're a safe, low-urgency nudge to
// remove and reclaim space.
func seededDoneCheck() check.Check {
	return check.Check{
		ID:      seededDoneID,
		Nodes:   check.NASOnly,
		Tier:    check.TierNudge,
		Cadence: check.Daily,
		Run:     runSeededDone,
	}
}

func runSeededDone(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.QBit == nil || (d.Sonarr == nil && d.Radarr == nil) {
		return check.Result{}, check.ErrNotConfigured
	}
	torrents, err := d.QBit.Torrents(ctx, "", "")
	if err != nil {
		return check.Result{}, fmt.Errorf("qbit torrents: %w", err)
	}
	prefs, err := d.QBit.Preferences(ctx)
	if err != nil {
		return check.Result{}, fmt.Errorf("qbit preferences: %w", err)
	}
	imported, err := importedDownloadIDs(ctx, d, d.Now().Add(-d.Cfg.Checks.ArrHistoryWindow))
	if err != nil {
		return check.Result{}, err
	}

	var findings []check.Finding
	for _, t := range torrents {
		if f := seededDoneFinding(d, t, prefs, imported); f != nil {
			findings = append(findings, *f)
		}
	}
	metrics := map[string]float64{"qbit_seeded_done": float64(len(findings))}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}

// seededDoneFinding applies the rule to a single torrent.
func seededDoneFinding(d check.Deps, t qbittorrent.Torrent, prefs qbittorrent.Preferences, imported map[string]bool) *check.Finding {
	if t.Progress < 1 || t.CompletionOn.IsZero() {
		return nil
	}
	if !seededToTarget(t, prefs) {
		return nil
	}
	if d.Now().Sub(t.CompletionOn) < d.Cfg.Checks.SeededMinAge {
		return nil
	}
	if !imported[strings.ToUpper(t.Hash)] {
		return nil
	}

	hours := int(t.SeedingTime.Round(time.Hour).Hours())
	f := d.NewFinding(seededDoneID, "qbit:"+t.Hash, check.SeverityInfo, check.TierNudge,
		fmt.Sprintf("%s seeded to target (ratio %.2f, %dh) and imported; safe to remove", t.Name, t.Ratio, hours))
	return &f
}

// seededToTarget reports whether t has met either of qBittorrent's own
// seeding targets. A zero/negative preference means that target is
// disabled, matching qBittorrent's own semantics.
func seededToTarget(t qbittorrent.Torrent, prefs qbittorrent.Preferences) bool {
	if prefs.MaxRatio > 0 && t.Ratio >= prefs.MaxRatio {
		return true
	}
	return prefs.MaxSeedingTime > 0 && t.SeedingTime >= time.Duration(prefs.MaxSeedingTime)*time.Minute
}
