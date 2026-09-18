package arr

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
)

const arrQueueStuckID = "arr_queue_stuck"

// queueItem is the shape common to sonarr.QueueItem and radarr.QueueItem
// (they differ only in carrying a SeriesID vs a MovieID, which this check
// doesn't need).
type queueItem struct {
	ID                    int64
	Title                 string
	TrackedDownloadStatus string
	TrackedDownloadState  string
	DownloadID            string
	SizeLeft              float64
	Messages              []string
	Added                 time.Time
}

// stuckStatuses are TrackedDownloadStatus values (compared case-insensitive)
// that mean the download itself is in trouble.
var stuckStatuses = map[string]bool{"warning": true, "error": true}

// stuckStates are TrackedDownloadState values that mean the app failed to
// import a completed download.
var stuckStates = map[string]bool{"importFailed": true, "failedPending": true, "importBlocked": true}

func sonarrQueueItems(items []sonarr.QueueItem) []queueItem {
	out := make([]queueItem, len(items))
	for i, r := range items {
		out[i] = queueItem{
			ID: r.ID, Title: r.Title, TrackedDownloadStatus: r.TrackedDownloadStatus, TrackedDownloadState: r.TrackedDownloadState,
			DownloadID: r.DownloadID, SizeLeft: r.SizeLeft, Messages: r.Messages, Added: r.Added,
		}
	}
	return out
}

func radarrQueueItems(items []radarr.QueueItem) []queueItem {
	out := make([]queueItem, len(items))
	for i, r := range items {
		out[i] = queueItem{
			ID: r.ID, Title: r.Title, TrackedDownloadStatus: r.TrackedDownloadStatus, TrackedDownloadState: r.TrackedDownloadState,
			DownloadID: r.DownloadID, SizeLeft: r.SizeLeft, Messages: r.Messages, Added: r.Added,
		}
	}
	return out
}

// arrQueueStuckCheck watches Sonarr's and Radarr's download queues for
// items the app has flagged as failed/blocked, or items that have been
// downloading far longer than expected.
func arrQueueStuckCheck() check.Check {
	return check.Check{
		ID:      arrQueueStuckID,
		Nodes:   check.PiOnly,
		Tier:    check.TierNudge,
		Cadence: check.Every15m,
		Run:     runArrQueueStuck,
	}
}

func runArrQueueStuck(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.Sonarr == nil && d.Radarr == nil {
		return check.Result{}, check.ErrNotConfigured
	}
	var findings []check.Finding
	metrics := map[string]float64{}

	if d.Sonarr != nil {
		raw, err := d.Sonarr.Queue(ctx)
		if err != nil {
			return check.Result{}, fmt.Errorf("sonarr queue: %w", err)
		}
		items := sonarrQueueItems(raw)
		metrics["sonarr_queue_len"] = float64(len(items))
		findings = append(findings, queueAppFindings(d, "sonarr", items)...)
	}
	if d.Radarr != nil {
		raw, err := d.Radarr.Queue(ctx)
		if err != nil {
			return check.Result{}, fmt.Errorf("radarr queue: %w", err)
		}
		items := radarrQueueItems(raw)
		metrics["radarr_queue_len"] = float64(len(items))
		findings = append(findings, queueAppFindings(d, "radarr", items)...)
	}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}

func queueAppFindings(d check.Deps, app string, items []queueItem) []check.Finding {
	var findings []check.Finding
	for _, it := range items {
		if f := queueItemFinding(d, app, it); f != nil {
			findings = append(findings, *f)
		}
	}
	return findings
}

// queueItemFinding applies the arr_queue_stuck rule to one queue item: a
// status/state the app itself flagged as failed wins first (it's the
// stronger signal), otherwise an item that's been downloading past the
// configured threshold with data still left to fetch is reported stuck.
func queueItemFinding(d check.Deps, app string, it queueItem) *check.Finding {
	key := app + ":queue:" + strconv.FormatInt(it.ID, 10)

	if stuckStatuses[strings.ToLower(it.TrackedDownloadStatus)] || stuckStates[it.TrackedDownloadState] {
		detail := it.TrackedDownloadState
		if len(it.Messages) > 0 {
			detail = it.Messages[0]
		}
		f := d.NewFinding(arrQueueStuckID, key, check.SeverityWarn, check.TierNudge,
			fmt.Sprintf("%s queue: %s — %s", app, it.Title, detail))
		f.Data = map[string]any{"downloadId": it.DownloadID, "state": it.TrackedDownloadState, "status": it.TrackedDownloadStatus}
		return &f
	}

	if !it.Added.IsZero() && d.Now().Sub(it.Added) > d.Cfg.Checks.QueueStuckAfter && it.SizeLeft > 0 {
		hours := d.Now().Sub(it.Added).Hours()
		f := d.NewFinding(arrQueueStuckID, key, check.SeverityWarn, check.TierNudge,
			fmt.Sprintf("%s queue: %s has been downloading for %.0fh", app, it.Title, hours))
		return &f
	}

	return nil
}
