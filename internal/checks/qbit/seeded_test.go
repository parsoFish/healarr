package qbit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

// prefsFailQBit delegates to an embedded *qbittorrent.Fake for everything
// except Preferences, which always fails.
type prefsFailQBit struct {
	*qbittorrent.Fake
	prefsErr error
}

func (f prefsFailQBit) Preferences(context.Context) (qbittorrent.Preferences, error) {
	f.Calls = append(f.Calls, "Preferences()")
	return qbittorrent.Preferences{}, f.prefsErr
}

func TestSeededDone(t *testing.T) {
	c := Checks(config.Config{})[3]
	cfg := testChecksCfg()
	prefs := qbittorrent.Preferences{MaxRatio: 2.0, MaxSeedingTime: 0}
	prefsSeedTime := qbittorrent.Preferences{MaxRatio: 0, MaxSeedingTime: 60} // minutes

	oldEnough := fixedNow.Add(-25 * time.Hour) // >= SeededMinAge (24h)
	tooRecent := fixedNow.Add(-1 * time.Hour)  // < SeededMinAge
	imported := &sonarr.Fake{HistoryRecords: []sonarr.HistoryRecord{
		{DownloadID: "aaa", EventType: "downloadFolderImported", Date: fixedNow.Add(-2 * time.Hour)},
	}}

	tests := []struct {
		name     string
		torrents []qbittorrent.Torrent
		prefs    qbittorrent.Preferences
		snr      sonarr.Client
		want     []wantFinding
	}{
		{
			name:     "not complete",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "Downloading", Progress: 0.5, Ratio: 3, CompletionOn: oldEnough}},
			prefs:    prefs,
			snr:      imported,
			want:     nil,
		},
		{
			name:     "completion time unset",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "No Completion", Progress: 1, Ratio: 3}},
			prefs:    prefs,
			snr:      imported,
			want:     nil,
		},
		{
			name:     "below ratio and seeding-time targets",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "Still Seeding", Progress: 1, Ratio: 0.5, CompletionOn: oldEnough}},
			prefs:    prefs,
			snr:      imported,
			want:     nil,
		},
		{
			name:     "met ratio target but too young",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "Fresh", Progress: 1, Ratio: 5, CompletionOn: tooRecent}},
			prefs:    prefs,
			snr:      imported,
			want:     nil,
		},
		{
			name:     "met ratio target, old enough, but not imported",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "Not Imported", Progress: 1, Ratio: 5, CompletionOn: oldEnough}},
			prefs:    prefs,
			snr:      &sonarr.Fake{},
			want:     nil,
		},
		{
			name:     "met ratio target, old enough, imported",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "Done Show", Progress: 1, Ratio: 5, CompletionOn: oldEnough}},
			prefs:    prefs,
			snr:      imported,
			want:     []wantFinding{{key: "qbit:AAA", sev: check.SeverityInfo}},
		},
		{
			name: "met seeding-time target, old enough, imported",
			torrents: []qbittorrent.Torrent{{
				Hash: "AAA", Name: "Long Seeder", Progress: 1, Ratio: 0.1,
				SeedingTime: 90 * time.Minute, CompletionOn: oldEnough,
			}},
			prefs: prefsSeedTime,
			snr:   imported,
			want:  []wantFinding{{key: "qbit:AAA", sev: check.SeverityInfo}},
		},
		{
			name: "both preference targets disabled",
			torrents: []qbittorrent.Torrent{{
				Hash: "AAA", Name: "No Targets", Progress: 1, Ratio: 100, SeedingTime: 999 * time.Hour, CompletionOn: oldEnough,
			}},
			prefs: qbittorrent.Preferences{MaxRatio: 0, MaxSeedingTime: 0},
			snr:   imported,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			qbit := &qbittorrent.Fake{TorrentList: tt.torrents, Prefs: tt.prefs}
			run(t, c, baseDeps(cfg, qbit, tt.snr, nil), tt.want, nil)
		})
	}
}

func TestSeededDoneNotConfiguredAndErrors(t *testing.T) {
	c := Checks(config.Config{})[3]
	cfg := testChecksCfg()

	t.Run("not configured: no qbit", func(t *testing.T) {
		run(t, c, baseDeps(cfg, nil, &sonarr.Fake{}, nil), nil, check.ErrNotConfigured)
	})
	t.Run("not configured: no sonarr or radarr", func(t *testing.T) {
		run(t, c, baseDeps(cfg, &qbittorrent.Fake{}, nil, nil), nil, check.ErrNotConfigured)
	})
	t.Run("torrents error", func(t *testing.T) {
		qbit := &qbittorrent.Fake{Err: errors.New("boom")}
		run(t, c, baseDeps(cfg, qbit, &sonarr.Fake{}, nil), nil, errAny)
	})
	t.Run("preferences error", func(t *testing.T) {
		qbit := prefsFailQBit{Fake: &qbittorrent.Fake{}, prefsErr: errors.New("prefs unavailable")}
		run(t, c, baseDeps(cfg, qbit, &sonarr.Fake{}, nil), nil, errAny)
	})
	t.Run("history error", func(t *testing.T) {
		qbit := &qbittorrent.Fake{TorrentList: []qbittorrent.Torrent{{Hash: "AAA"}}}
		snr := &sonarr.Fake{Err: errors.New("sonarr down")}
		run(t, c, baseDeps(cfg, qbit, snr, nil), nil, errAny)
	})
	t.Run("imported via radarr only", func(t *testing.T) {
		rdr := &radarr.Fake{HistoryRecords: []radarr.HistoryRecord{
			{DownloadID: "aaa", EventType: "downloadFolderImported", Date: fixedNow.Add(-2 * time.Hour)},
		}}
		qbit := &qbittorrent.Fake{
			TorrentList: []qbittorrent.Torrent{{Hash: "AAA", Name: "Movie", Progress: 1, Ratio: 5, CompletionOn: fixedNow.Add(-25 * time.Hour)}},
			Prefs:       qbittorrent.Preferences{MaxRatio: 2.0},
		}
		run(t, c, baseDeps(cfg, qbit, nil, rdr), []wantFinding{{key: "qbit:AAA", sev: check.SeverityInfo}}, nil)
	})
}

// TestSeededDoneSummaryAndMetric pins the summary text and the
// qbit_seeded_done metric.
func TestSeededDoneSummaryAndMetric(t *testing.T) {
	c := Checks(config.Config{})[3]
	cfg := testChecksCfg()
	imported := &sonarr.Fake{HistoryRecords: []sonarr.HistoryRecord{
		{DownloadID: "aaa", EventType: "downloadFolderImported", Date: fixedNow.Add(-2 * time.Hour)},
	}}
	torrents := []qbittorrent.Torrent{{
		Hash: "AAA", Name: "Done Show", Progress: 1, Ratio: 2.5,
		SeedingTime: 48 * time.Hour, CompletionOn: fixedNow.Add(-25 * time.Hour),
	}}
	qbit := &qbittorrent.Fake{TorrentList: torrents, Prefs: qbittorrent.Preferences{MaxRatio: 2.0}}
	res := run(t, c, baseDeps(cfg, qbit, imported, nil), []wantFinding{{key: "qbit:AAA", sev: check.SeverityInfo}}, nil)

	wantSummary := "Done Show seeded to target (ratio 2.50, 48h) and imported; safe to remove"
	if got := res.Findings[0].Summary; got != wantSummary {
		t.Errorf("summary = %q, want %q", got, wantSummary)
	}
	if got := res.Metrics["qbit_seeded_done"]; got != 1 {
		t.Errorf("qbit_seeded_done = %v, want 1", got)
	}
}
