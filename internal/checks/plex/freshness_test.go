package plex

import (
	"context"
	"errors"
	"strings"
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
	// A new episode lands in <Library>/<Show>/<Season NN>/, which bumps the
	// season directory's mtime two levels below the library path.
	seasonEntries := map[string][]hostfs.Entry{
		"/volume1/tv":            {{Name: "Show", IsDir: true, ModTime: scannedAt.Add(-48 * time.Hour)}},
		"/volume1/tv/Show":       {{Name: "Season 01", IsDir: true, ModTime: scannedAt.Add(5 * time.Hour)}},
		"/volume1/tv/Show/Extra": {{Name: "never-read.mkv", ModTime: scannedAt.Add(100 * time.Hour)}},
	}
	libs := []plex.Library{{Key: "1", Title: "TV Shows", ScannedAt: scannedAt}}
	neverScanned := []plex.Library{{Key: "1", Title: "TV Shows"}}

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
			// Two levels down, because that is where the season directory
			// whose mtime moved actually lives.
			name: "stale via season subdirectory",
			deps: baseDeps(cfg, &plex.Fake{LibraryList: libs}, &hostfs.Fake{Entries: seasonEntries}),
			want: []wantFinding{{key: "plex:1", sev: check.SeverityWarn}},
		},
		{
			name: "never scanned",
			deps: baseDeps(cfg, &plex.Fake{LibraryList: neverScanned}, &hostfs.Fake{Entries: freshEntries}),
			want: []wantFinding{{key: "plex:1", sev: check.SeverityWarn}},
		},
		{
			// An empty library directory says nothing about scan
			// freshness: there is no file Plex could have missed.
			name: "empty library directory",
			deps: baseDeps(cfg, &plex.Fake{LibraryList: libs}, &hostfs.Fake{Entries: map[string][]hostfs.Entry{}}),
			want: nil,
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
			case "stale", "stale via season subdirectory":
				want := "plex library TV Shows last scanned 2026-09-18T05:00:00Z but /volume1/tv changed 2026-09-18T10:00:00Z"
				if got := res.Findings[0].Summary; got != want {
					t.Fatalf("summary = %q, want %q", got, want)
				}
			case "never scanned":
				want := "plex library TV Shows has never been scanned"
				if got := res.Findings[0].Summary; got != want {
					t.Fatalf("summary = %q, want %q", got, want)
				}
				if _, ok := res.Metrics["plex_scan_age_hours:1"]; ok {
					t.Fatalf("a never-scanned library must not report an age metric: %v", res.Metrics)
				}
			case "empty library directory":
				if _, ok := res.Metrics["plex_scan_age_hours:1"]; ok {
					t.Fatalf("an empty library directory must not report an age metric: %v", res.Metrics)
				}
			}
		})
	}
}

// subdirFailHost delegates to an embedded *hostfs.Fake except for one
// path's ListDir, which always fails. hostfs.Fake's single Err field fails
// every call uniformly, so it can't express "the library dir lists, a
// subdirectory does not" on its own.
type subdirFailHost struct {
	*hostfs.Fake
	failPath string
	err      error
}

func (h subdirFailHost) ListDir(ctx context.Context, path string) ([]hostfs.Entry, error) {
	if path == h.failPath {
		return nil, h.err
	}
	return h.Fake.ListDir(ctx, path)
}

// TestPlexScanFreshnessSubdirListDirError proves a subdirectory the check
// cannot read fails the check loudly rather than being read as "nothing
// changed down there".
func TestPlexScanFreshnessSubdirListDirError(t *testing.T) {
	c := Checks(config.Config{})[1]
	cfg := config.Config{Checks: config.Checks{
		PlexLibraries:      []config.PlexLibrary{{Title: "TV Shows", Path: "/volume1/tv"}},
		PlexScanStaleAfter: 2 * time.Hour,
	}}
	scannedAt := time.Date(2026, 9, 18, 5, 0, 0, 0, time.UTC)
	host := subdirFailHost{
		Fake: &hostfs.Fake{Entries: map[string][]hostfs.Entry{
			"/volume1/tv": {{Name: "Show", IsDir: true, ModTime: scannedAt}},
		}},
		failPath: "/volume1/tv/Show",
		err:      errors.New("permission denied"),
	}
	libs := []plex.Library{{Key: "1", Title: "TV Shows", ScannedAt: scannedAt}}

	_, err := c.Run(context.Background(), baseDeps(cfg, &plex.Fake{LibraryList: libs}, host)())
	if err == nil {
		t.Fatal("expected the subdirectory ListDir error to be returned")
	}
	if !strings.Contains(err.Error(), "/volume1/tv/Show") {
		t.Errorf("error = %v, want it to name the subdirectory", err)
	}
}
