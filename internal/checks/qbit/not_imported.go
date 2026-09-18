package qbit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
)

const notImportedID = "qbit_completed_not_imported"

// completedNotImportedCheck flags torrents that finished downloading a
// while ago but never showed up as a "downloadFolderImported" event in
// Sonarr or Radarr's history: the arr never picked the file up, so it sits
// in the download client forever without reaching the library.
func completedNotImportedCheck() check.Check {
	return check.Check{
		ID:      notImportedID,
		Nodes:   check.NASOnly,
		Tier:    check.TierCorrect,
		Cadence: check.Every15m,
		Run:     runCompletedNotImported,
	}
}

func runCompletedNotImported(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.QBit == nil || (d.Sonarr == nil && d.Radarr == nil) {
		return check.Result{}, check.ErrNotConfigured
	}
	torrents, err := d.QBit.Torrents(ctx, "", "")
	if err != nil {
		return check.Result{}, fmt.Errorf("qbit torrents: %w", err)
	}
	imported, err := importedDownloadIDs(ctx, d, d.Now().Add(-d.Cfg.Checks.ArrHistoryWindow))
	if err != nil {
		return check.Result{}, err
	}

	var findings []check.Finding
	for _, t := range torrents {
		if f := notImportedFinding(d, t, imported); f != nil {
			findings = append(findings, *f)
		}
	}
	return check.Result{Findings: findings}, nil
}

// notImportedFinding applies the rule to a single torrent. A torrent older
// than ArrHistoryWindow is skipped rather than flagged: the history query
// above only reaches back that far, so absence of an import record for an
// older torrent proves nothing.
func notImportedFinding(d check.Deps, t qbittorrent.Torrent, imported map[string]bool) *check.Finding {
	if t.Progress < 1 || t.Category == "" || t.CompletionOn.IsZero() {
		return nil
	}
	age := d.Now().Sub(t.CompletionOn)
	if age > d.Cfg.Checks.ArrHistoryWindow || age <= d.Cfg.Checks.CompletedNotImportedAfter {
		return nil
	}
	if imported[strings.ToUpper(t.Hash)] {
		return nil
	}

	f := d.NewFinding(notImportedID, "qbit:"+t.Hash, check.SeverityWarn, check.TierCorrect,
		fmt.Sprintf("%s completed %s ago but no arr import recorded", t.Name, age.Round(time.Minute)))
	f.Data = map[string]any{"category": t.Category, "contentPath": t.ContentPath, "completedAt": t.CompletionOn}
	return &f
}
