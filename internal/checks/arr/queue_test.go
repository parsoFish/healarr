package arr

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

func queueCfg() config.Config {
	return config.Config{Checks: config.Checks{QueueStuckAfter: 24 * time.Hour}}
}

func TestArrQueueStuck(t *testing.T) {
	c := Checks(config.Config{})[1] // arr_queue_stuck is the family's second check

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "warning status is stuck",
			deps: baseDeps(queueCfg(), &sonarr.Fake{QueueItems: []sonarr.QueueItem{
				{ID: 1, Title: "Show S01E01", TrackedDownloadStatus: "warning", Added: fixedNow.Add(-time.Hour)},
			}}, nil, nil),
			want: []wantFinding{{key: "sonarr:queue:1", sev: check.SeverityWarn}},
		},
		{
			name: "importFailed state is stuck",
			deps: baseDeps(queueCfg(), &sonarr.Fake{QueueItems: []sonarr.QueueItem{
				{ID: 2, Title: "Show S01E02", TrackedDownloadState: "importFailed", Added: fixedNow.Add(-time.Hour)},
			}}, nil, nil),
			want: []wantFinding{{key: "sonarr:queue:2", sev: check.SeverityWarn}},
		},
		{
			name: "slow old item still has data left",
			deps: baseDeps(queueCfg(), &sonarr.Fake{QueueItems: []sonarr.QueueItem{
				{ID: 3, Title: "Show S01E03", TrackedDownloadStatus: "ok", SizeLeft: 500, Added: fixedNow.Add(-30 * time.Hour)},
			}}, nil, nil),
			want: []wantFinding{{key: "sonarr:queue:3", sev: check.SeverityWarn}},
		},
		{
			name: "fresh healthy item - no finding",
			deps: baseDeps(queueCfg(), &sonarr.Fake{QueueItems: []sonarr.QueueItem{
				{ID: 4, Title: "Show S01E04", TrackedDownloadStatus: "ok", SizeLeft: 500, Added: fixedNow.Add(-time.Hour)},
			}}, nil, nil),
			want: nil,
		},
		{
			name: "old item with nothing left to download - no finding",
			deps: baseDeps(queueCfg(), &sonarr.Fake{QueueItems: []sonarr.QueueItem{
				{ID: 5, Title: "Show S01E05", TrackedDownloadStatus: "ok", SizeLeft: 0, Added: fixedNow.Add(-30 * time.Hour)},
			}}, nil, nil),
			want: nil,
		},
		{
			name: "radarr-only",
			deps: baseDeps(queueCfg(), nil, &radarr.Fake{QueueItems: []radarr.QueueItem{
				{ID: 6, Title: "Movie", TrackedDownloadStatus: "error", Added: fixedNow.Add(-time.Hour)},
			}}, nil),
			want: []wantFinding{{key: "radarr:queue:6", sev: check.SeverityWarn}},
		},
		{
			name:    "sonarr client error",
			deps:    baseDeps(queueCfg(), &sonarr.Fake{Err: errors.New("connection refused")}, nil, nil),
			wantErr: errAny,
		},
		{
			name:    "radarr client error",
			deps:    baseDeps(queueCfg(), nil, &radarr.Fake{Err: errors.New("connection refused")}, nil),
			wantErr: errAny,
		},
		{
			name:    "not configured",
			deps:    baseDeps(queueCfg(), nil, nil, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run(t, c, tt.deps, tt.want, tt.wantErr)
		})
	}
}

func TestArrQueueStuckMetrics(t *testing.T) {
	c := Checks(config.Config{})[1]
	deps := baseDeps(queueCfg(),
		&sonarr.Fake{QueueItems: []sonarr.QueueItem{{ID: 1, Added: fixedNow}, {ID: 2, Added: fixedNow}}},
		&radarr.Fake{QueueItems: []radarr.QueueItem{{ID: 1, Added: fixedNow}}},
		nil,
	)
	res, err := c.Run(context.Background(), deps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Metrics["sonarr_queue_len"] != 2 {
		t.Fatalf("sonarr_queue_len = %v, want 2", res.Metrics["sonarr_queue_len"])
	}
	if res.Metrics["radarr_queue_len"] != 1 {
		t.Fatalf("radarr_queue_len = %v, want 1", res.Metrics["radarr_queue_len"])
	}
}

func TestArrQueueStuckFindingData(t *testing.T) {
	c := Checks(config.Config{})[1]
	deps := baseDeps(queueCfg(), &sonarr.Fake{QueueItems: []sonarr.QueueItem{
		{ID: 7, Title: "Show S01E07", TrackedDownloadStatus: "warning", TrackedDownloadState: "importFailed", DownloadID: "abc123", Messages: []string{"disk full"}, Added: fixedNow.Add(-time.Hour)},
	}}, nil, nil)
	res, err := c.Run(context.Background(), deps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want 1", res.Findings)
	}
	f := res.Findings[0]
	wantSummary := "sonarr queue: Show S01E07 — disk full"
	if f.Summary != wantSummary {
		t.Fatalf("summary = %q, want %q", f.Summary, wantSummary)
	}
	if f.Data["downloadId"] != "abc123" || f.Data["state"] != "importFailed" || f.Data["status"] != "warning" {
		t.Fatalf("data = %+v, want downloadId=abc123 state=importFailed status=warning", f.Data)
	}
}
