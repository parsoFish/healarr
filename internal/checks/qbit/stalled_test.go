package qbit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/config"
)

func TestStalledErrored(t *testing.T) {
	c := Checks(config.Config{})[0]
	cfg := testChecksCfg()

	tests := []struct {
		name     string
		torrents []qbittorrent.Torrent
		want     []wantFinding
		wantErr  error
	}{
		{
			name:     "error state",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "Movie", State: "error"}},
			want:     []wantFinding{{key: "qbit:AAA", sev: check.SeverityCritical}},
		},
		{
			name:     "missingFiles state",
			torrents: []qbittorrent.Torrent{{Hash: "BBB", Name: "Show", State: "missingFiles"}},
			want:     []wantFinding{{key: "qbit:BBB", sev: check.SeverityCritical}},
		},
		{
			name: "stalledDL past threshold",
			torrents: []qbittorrent.Torrent{{
				Hash: "CCC", Name: "Stalled Show", State: "stalledDL",
				AddedOn: fixedNow.Add(-2 * time.Hour), NumSeeds: 0,
			}},
			want: []wantFinding{{key: "qbit:CCC", sev: check.SeverityWarn}},
		},
		{
			name: "stalledDL within threshold",
			torrents: []qbittorrent.Torrent{{
				Hash: "DDD", Name: "Recent Show", State: "stalledDL", AddedOn: fixedNow.Add(-10 * time.Minute),
			}},
			want: nil,
		},
		{
			name: "metaDL past threshold",
			torrents: []qbittorrent.Torrent{{
				Hash: "EEE", Name: "Meta Show", State: "metaDL", AddedOn: fixedNow.Add(-3 * time.Hour),
			}},
			want: []wantFinding{{key: "qbit:EEE", sev: check.SeverityWarn}},
		},
		{
			name:     "stalledDL with zero AddedOn is not flagged",
			torrents: []qbittorrent.Torrent{{Hash: "FFF", Name: "No Added", State: "stalledDL"}},
			want:     nil,
		},
		{
			name: "stoppedDL (qbit5 paused) is not flagged",
			torrents: []qbittorrent.Torrent{{
				Hash: "GGG", Name: "Paused", State: "stoppedDL", AddedOn: fixedNow.Add(-5 * time.Hour),
			}},
			want: nil,
		},
		{
			name: "stoppedUP (qbit5 paused seed) is not flagged",
			torrents: []qbittorrent.Torrent{{
				Hash: "HHH", Name: "Paused Seed", State: "stoppedUP", AddedOn: fixedNow.Add(-5 * time.Hour),
			}},
			want: nil,
		},
		{
			name:     "healthy downloading torrent",
			torrents: []qbittorrent.Torrent{{Hash: "III", Name: "Downloading", State: "downloading"}},
			want:     nil,
		},
		{
			name:    "not configured",
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "client error",
			wantErr: errAny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var deps func() check.Deps
			switch tt.name {
			case "not configured":
				deps = baseDeps(cfg, nil, nil, nil)
			case "client error":
				deps = baseDeps(cfg, &qbittorrent.Fake{Err: errors.New("boom")}, nil, nil)
			default:
				deps = baseDeps(cfg, &qbittorrent.Fake{TorrentList: tt.torrents}, nil, nil)
			}
			run(t, c, deps, tt.want, tt.wantErr)
		})
	}
}

// TestStalledErroredSummaries pins the exact summary text for both the
// error-state and the stalled-state branches.
func TestStalledErroredSummaries(t *testing.T) {
	c := Checks(config.Config{})[0]
	cfg := testChecksCfg()
	torrents := []qbittorrent.Torrent{
		{Hash: "AAA", Name: "Broken Movie", State: "error"},
		{Hash: "CCC", Name: "Stalled Show", State: "stalledDL", AddedOn: fixedNow.Add(-2 * time.Hour), NumSeeds: 3},
	}
	res, err := c.Run(context.Background(), baseDeps(cfg, &qbittorrent.Fake{TorrentList: torrents}, nil, nil)())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byKey := map[string]check.Finding{}
	for _, f := range res.Findings {
		byKey[f.EntityKey] = f
	}
	if got, want := byKey["qbit:AAA"].Summary, "Broken Movie is in state error"; got != want {
		t.Errorf("error summary = %q, want %q", got, want)
	}
	wantStalled := "Stalled Show stalled (3 seeds, added " + fixedNow.Add(-2*time.Hour).Format(time.RFC3339) + ")"
	if got := byKey["qbit:CCC"].Summary; got != wantStalled {
		t.Errorf("stalled summary = %q, want %q", got, wantStalled)
	}
}

// TestStalledErroredMetrics checks qbit_torrents and qbit_stalled are
// computed across the whole torrent list, not per-finding.
func TestStalledErroredMetrics(t *testing.T) {
	c := Checks(config.Config{})[0]
	cfg := testChecksCfg()
	torrents := []qbittorrent.Torrent{
		{Hash: "AAA", Name: "Broken", State: "error"},
		{Hash: "BBB", Name: "Stalled", State: "stalledDL", AddedOn: fixedNow.Add(-2 * time.Hour)},
		{Hash: "CCC", Name: "Healthy", State: "downloading"},
	}
	res, err := c.Run(context.Background(), baseDeps(cfg, &qbittorrent.Fake{TorrentList: torrents}, nil, nil)())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := res.Metrics["qbit_torrents"]; got != 3 {
		t.Errorf("qbit_torrents = %v, want 3", got)
	}
	if got := res.Metrics["qbit_stalled"]; got != 2 {
		t.Errorf("qbit_stalled = %v, want 2", got)
	}
}
