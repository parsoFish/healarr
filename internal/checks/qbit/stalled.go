package qbit

import (
	"context"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
)

const stalledErroredID = "qbit_stalled_errored"

// stalledErroredCheck flags torrents qBittorrent itself has marked as
// broken (error, missingFiles) and torrents that have been stuck
// downloading metadata or stalled without progress for longer than
// QBitStalledAfter. qBittorrent 5 renamed the paused states to
// stoppedDL/stoppedUP; those are a normal, intentional state, not a
// problem, and are deliberately not flagged here.
func stalledErroredCheck() check.Check {
	return check.Check{
		ID:      stalledErroredID,
		Nodes:   check.NASOnly,
		Tier:    check.TierNudge,
		Cadence: check.Every15m,
		Run:     runStalledErrored,
	}
}

func runStalledErrored(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.QBit == nil {
		return check.Result{}, check.ErrNotConfigured
	}
	torrents, err := d.QBit.Torrents(ctx, "", "")
	if err != nil {
		return check.Result{}, fmt.Errorf("qbit torrents: %w", err)
	}

	var findings []check.Finding
	for _, t := range torrents {
		if f := stalledErroredFinding(d, t); f != nil {
			findings = append(findings, *f)
		}
	}
	metrics := map[string]float64{
		"qbit_torrents": float64(len(torrents)),
		"qbit_stalled":  float64(len(findings)),
	}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}

// stalledErroredFinding applies the rule to a single torrent: qBittorrent's
// own error states are always critical; a torrent stuck stalled or
// fetching metadata is only a warning once it has had time to recover on
// its own (QBitStalledAfter).
func stalledErroredFinding(d check.Deps, t qbittorrent.Torrent) *check.Finding {
	key := "qbit:" + t.Hash
	switch t.State {
	case "error", "missingFiles":
		f := d.NewFinding(stalledErroredID, key, check.SeverityCritical, check.TierNudge,
			fmt.Sprintf("%s is in state %s", t.Name, t.State))
		return &f
	case "stalledDL", "metaDL":
		if !t.AddedOn.IsZero() && d.Now().Sub(t.AddedOn) > d.Cfg.Checks.QBitStalledAfter {
			f := d.NewFinding(stalledErroredID, key, check.SeverityWarn, check.TierNudge,
				fmt.Sprintf("%s stalled (%d seeds, added %s)", t.Name, t.NumSeeds, t.AddedOn.Format(time.RFC3339)))
			return &f
		}
	}
	return nil
}
