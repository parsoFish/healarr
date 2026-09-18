package qbit

import (
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

func TestCompletedNotImported(t *testing.T) {
	c := Checks(config.Config{})[1]
	cfg := testChecksCfg()

	oldEnough := fixedNow.Add(-2 * time.Hour)                          // > CompletedNotImportedAfter (30m)
	tooRecent := fixedNow.Add(-10 * time.Minute)                       // < CompletedNotImportedAfter
	outsideWindow := fixedNow.Add(-(cfg.ArrHistoryWindow + time.Hour)) // > ArrHistoryWindow (7d)

	sonarrImportedAAA := &sonarr.Fake{HistoryRecords: []sonarr.HistoryRecord{
		{DownloadID: "aaa", EventType: "downloadFolderImported", Date: fixedNow.Add(-time.Hour)},
	}}
	radarrImportedZZZ := &radarr.Fake{HistoryRecords: []radarr.HistoryRecord{
		{DownloadID: "zzz", EventType: "downloadFolderImported", Date: fixedNow.Add(-time.Hour)},
	}}

	tests := []struct {
		name     string
		torrents []qbittorrent.Torrent
		qbitErr  error
		snr      sonarr.Client
		rdr      radarr.Client
		want     []wantFinding
		wantErr  error
	}{
		{
			name:     "not yet complete",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "In Progress", Progress: 0.5, Category: "tv", CompletionOn: oldEnough}},
			snr:      &sonarr.Fake{},
			want:     nil,
		},
		{
			name:     "no category",
			torrents: []qbittorrent.Torrent{{Hash: "BBB", Name: "No Category", Progress: 1, CompletionOn: oldEnough}},
			snr:      &sonarr.Fake{},
			want:     nil,
		},
		{
			name:     "completion time unset",
			torrents: []qbittorrent.Torrent{{Hash: "CCC", Name: "No Completion", Progress: 1, Category: "tv"}},
			snr:      &sonarr.Fake{},
			want:     nil,
		},
		{
			name:     "completed too recently to worry",
			torrents: []qbittorrent.Torrent{{Hash: "DDD", Name: "Fresh", Progress: 1, Category: "tv", CompletionOn: tooRecent}},
			snr:      &sonarr.Fake{},
			want:     nil,
		},
		{
			name:     "imported via sonarr history",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "Imported Show", Progress: 1, Category: "tv", CompletionOn: oldEnough}},
			snr:      sonarrImportedAAA,
			want:     nil,
		},
		{
			name:     "imported via radarr history",
			torrents: []qbittorrent.Torrent{{Hash: "ZZZ", Name: "Imported Movie", Progress: 1, Category: "movies", CompletionOn: oldEnough}},
			rdr:      radarrImportedZZZ,
			want:     nil,
		},
		{
			name:     "completed long ago, never imported",
			torrents: []qbittorrent.Torrent{{Hash: "EEE", Name: "Stuck Show", Progress: 1, Category: "tv", CompletionOn: oldEnough}},
			snr:      &sonarr.Fake{},
			want:     []wantFinding{{key: "qbit:EEE", sev: check.SeverityWarn}},
		},
		{
			name:     "older than arr history window is skipped",
			torrents: []qbittorrent.Torrent{{Hash: "FFF", Name: "Ancient", Progress: 1, Category: "tv", CompletionOn: outsideWindow}},
			snr:      &sonarr.Fake{},
			want:     nil,
		},
		{
			name:    "not configured: no qbit",
			snr:     &sonarr.Fake{},
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "not configured: no sonarr or radarr",
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "qbit torrents error",
			qbitErr: errors.New("boom"),
			snr:     &sonarr.Fake{},
			wantErr: errAny,
		},
		{
			name:     "sonarr history error",
			torrents: []qbittorrent.Torrent{{Hash: "GGG", Progress: 1, Category: "tv", CompletionOn: oldEnough}},
			snr:      &sonarr.Fake{Err: errors.New("sonarr down")},
			wantErr:  errAny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var qbit qbittorrent.Client
			switch tt.name {
			case "not configured: no qbit":
				qbit = nil
			default:
				qbit = &qbittorrent.Fake{TorrentList: tt.torrents, Err: tt.qbitErr}
			}
			deps := baseDeps(cfg, qbit, tt.snr, tt.rdr)
			run(t, c, deps, tt.want, tt.wantErr)
		})
	}
}

// TestCompletedNotImportedData pins the Data payload and summary shape for
// a stuck torrent.
func TestCompletedNotImportedData(t *testing.T) {
	c := Checks(config.Config{})[1]
	cfg := testChecksCfg()
	completedAt := fixedNow.Add(-2 * time.Hour)
	torrents := []qbittorrent.Torrent{{
		Hash: "EEE", Name: "Stuck Show", Progress: 1, Category: "tv",
		ContentPath: "/downloads/Stuck.Show.S01E01", CompletionOn: completedAt,
	}}
	res := run(t, c, baseDeps(cfg, &qbittorrent.Fake{TorrentList: torrents}, &sonarr.Fake{}, nil),
		[]wantFinding{{key: "qbit:EEE", sev: check.SeverityWarn}}, nil)

	f := res.Findings[0]
	wantData := map[string]any{"category": "tv", "contentPath": "/downloads/Stuck.Show.S01E01", "completedAt": completedAt}
	for k, want := range wantData {
		if got := f.Data[k]; got != want {
			t.Errorf("Data[%s] = %v, want %v", k, got, want)
		}
	}
	wantSummary := "Stuck Show completed 2h0m0s ago but no arr import recorded"
	if f.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", f.Summary, wantSummary)
	}
}
