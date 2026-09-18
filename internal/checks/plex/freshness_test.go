package plex

import (
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/plex"
	"github.com/parsoFish/healarr/internal/config"
)

func TestPlexScanFreshness(t *testing.T) {
	c := Checks(config.Config{})[1] // plex_scan_freshness is the family's second check

	cfg := config.Config{Checks: config.Checks{
		PlexLibraries:      []config.PlexLibrary{{Title: "TV Shows", Path: "/volume1/tv"}},
		PlexScanStaleAfter: 2 * time.Hour,
	}}
	scannedAt := time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)
	freshEntries := map[string][]hostfs.Entry{
		"/volume1/tv": {{Name: "show.mkv", ModTime: scannedAt.Add(30 * time.Minute)}},
	}
	staleEntries := map[string][]hostfs.Entry{
		"/volume1/tv": {{Name: "show.mkv", ModTime: scannedAt.Add(5 * time.Hour)}},
	}
	libs := []plex.Library{{Key: "1", Title: "TV Shows", ScannedAt: scannedAt}}

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "fresh",
			deps: baseDeps(cfg, &plex.Fake{LibraryList: libs}, &hostfs.Fake{Entries: freshEntries}),
			want: nil,
		},
		{
			name: "library not found",
			deps: baseDeps(cfg,
				&plex.Fake{LibraryList: []plex.Library{{Key: "9", Title: "Movies", ScannedAt: scannedAt}}},
				&hostfs.Fake{Entries: freshEntries}),
			want: []wantFinding{{key: "plex:TV Shows", sev: check.SeverityWarn}},
		},
		{
			name: "stale",
			deps: baseDeps(cfg, &plex.Fake{LibraryList: libs}, &hostfs.Fake{Entries: staleEntries}),
			want: []wantFinding{{key: "plex:1", sev: check.SeverityWarn}},
		},
		{
			name:    "libraries error",
			deps:    baseDeps(cfg, &plex.Fake{Err: errors.New("plex down")}, &hostfs.Fake{Entries: freshEntries}),
			wantErr: errAny,
		},
		{
			name:    "listdir error",
			deps:    baseDeps(cfg, &plex.Fake{LibraryList: libs}, &hostfs.Fake{Err: errors.New("nas offline")}),
			wantErr: errAny,
		},
		{
			name:    "not configured: no libraries",
			deps:    baseDeps(config.Config{}, &plex.Fake{}, &hostfs.Fake{}),
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "not configured: no plex client",
			deps:    baseDeps(cfg, nil, &hostfs.Fake{}),
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "not configured: no host client",
			deps:    baseDeps(cfg, &plex.Fake{}, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, c, tt.deps, tt.want, tt.wantErr)
			switch tt.name {
			case "fresh":
				if got := res.Metrics["plex_scan_age_hours:1"]; got != 0.5 {
					t.Fatalf("plex_scan_age_hours:1 = %v, want 0.5", got)
				}
			case "stale":
				want := "plex library TV Shows last scanned 2026-09-18T05:00:00Z but /volume1/tv changed 2026-09-18T10:00:00Z"
				if got := res.Findings[0].Summary; got != want {
					t.Fatalf("summary = %q, want %q", got, want)
				}
			}
		})
	}
}
