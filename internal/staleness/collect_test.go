package staleness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/clients/tautulli"
	"github.com/parsoFish/healarr/internal/config"
)

// collectDeps builds a check.Deps for Collect's tests: a fixed clock and
// node, given clients (any of which may be nil), and a Staleness.DaysHorizon
// wide enough that the fixture history rows fall inside the lookback window.
func collectDeps(sc sonarr.Client, rc radarr.Client, tc tautulli.Client, oc overseerr.Client) check.Deps {
	return check.Deps{
		Node:      config.NodePi,
		Cfg:       config.Config{Staleness: config.Staleness{DaysHorizon: 180}},
		Now:       func() time.Time { return fixedNow },
		Sonarr:    sc,
		Radarr:    rc,
		Tautulli:  tc,
		Overseerr: oc,
	}
}

func TestCollectRequiresSonarrOrRadarrAndTautulli(t *testing.T) {
	tc := &tautulli.Fake{}
	sc := &sonarr.Fake{}

	if _, err := Collect(context.Background(), collectDeps(nil, nil, tc, nil)); !errors.Is(err, check.ErrNotConfigured) {
		t.Fatalf("neither sonarr nor radarr: err = %v, want ErrNotConfigured", err)
	}
	if _, err := Collect(context.Background(), collectDeps(sc, nil, nil, nil)); !errors.Is(err, check.ErrNotConfigured) {
		t.Fatalf("no tautulli: err = %v, want ErrNotConfigured", err)
	}
}

func TestCollectJoinsSeriesWithHistoryAndOverseerr(t *testing.T) {
	sc := &sonarr.Fake{SeriesList: []sonarr.Series{
		{ID: 1, TVDBID: 100, Title: "Breaking Bad", Monitored: true, Status: "ended", SizeOnDisk: 5_000_000_000, Added: fixedNow.Add(-500 * 24 * time.Hour)},
	}}
	tc := &tautulli.Fake{HistoryRows: []tautulli.HistoryRow{
		// Case-insensitive match against the series title, via
		// GrandparentTitle (the show an episode belongs to).
		{MediaType: "episode", GrandparentTitle: "breaking bad", Date: fixedNow.Add(-10 * 24 * time.Hour), WatchedStatus: 1},
		{MediaType: "episode", GrandparentTitle: "breaking bad", Date: fixedNow.Add(-40 * 24 * time.Hour), WatchedStatus: 0},
	}}
	oc := &overseerr.Fake{RequestList: []overseerr.Request{
		{TVDBID: 100, RequestedBy: "alice@example.com"},
	}}

	items, err := Collect(context.Background(), collectDeps(sc, nil, tc, oc))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1: %+v", len(items), items)
	}
	it := items[0]
	if it.EntityKey != "sonarr:1" {
		t.Errorf("EntityKey = %q, want sonarr:1", it.EntityKey)
	}
	if it.Kind != "series" {
		t.Errorf("Kind = %q, want series", it.Kind)
	}
	if !it.WatchedFully || it.WatchedPartially {
		t.Errorf("WatchedFully/Partially = %v/%v, want true/false", it.WatchedFully, it.WatchedPartially)
	}
	if !it.LastWatched.Equal(fixedNow.Add(-10 * 24 * time.Hour)) {
		t.Errorf("LastWatched = %v, want the most recent row", it.LastWatched)
	}
	if it.RequestedBy != "alice@example.com" {
		t.Errorf("RequestedBy = %q, want alice@example.com", it.RequestedBy)
	}
	if !it.Ended {
		t.Errorf("Ended = false, want true (Status==ended)")
	}
	if !it.Monitored {
		t.Errorf("Monitored = false, want true")
	}
	if it.SizeBytes != 5_000_000_000 {
		t.Errorf("SizeBytes = %d, want 5_000_000_000", it.SizeBytes)
	}
}

func TestCollectPartiallyWatchedSeries(t *testing.T) {
	sc := &sonarr.Fake{SeriesList: []sonarr.Series{
		{ID: 2, TVDBID: 200, Title: "Partial Show", Status: "continuing"},
	}}
	tc := &tautulli.Fake{HistoryRows: []tautulli.HistoryRow{
		{MediaType: "episode", GrandparentTitle: "Partial Show", Date: fixedNow.Add(-3 * 24 * time.Hour), WatchedStatus: 0, PercentComplete: 40},
	}}

	items, err := Collect(context.Background(), collectDeps(sc, nil, tc, nil))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	it := items[0]
	if it.WatchedFully {
		t.Errorf("WatchedFully = true, want false")
	}
	if !it.WatchedPartially {
		t.Errorf("WatchedPartially = false, want true")
	}
	if it.Ended {
		t.Errorf("Ended = true, want false (Status==continuing)")
	}
}

func TestCollectNeverWatchedSeriesHasNoHistoryMatch(t *testing.T) {
	sc := &sonarr.Fake{SeriesList: []sonarr.Series{
		{ID: 3, TVDBID: 300, Title: "Unwatched Show", Status: "continuing"},
	}}
	tc := &tautulli.Fake{HistoryRows: []tautulli.HistoryRow{
		{MediaType: "episode", GrandparentTitle: "Some Other Show", Date: fixedNow},
	}}

	items, err := Collect(context.Background(), collectDeps(sc, nil, tc, nil))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	it := items[0]
	if it.WatchedFully || it.WatchedPartially {
		t.Errorf("WatchedFully/Partially = %v/%v, want false/false", it.WatchedFully, it.WatchedPartially)
	}
	if !it.LastWatched.IsZero() {
		t.Errorf("LastWatched = %v, want zero", it.LastWatched)
	}
}

func TestCollectJoinsMovieWithHistoryAndOverseerr(t *testing.T) {
	rc := &radarr.Fake{MovieList: []radarr.Movie{
		{ID: 10, TMDBID: 603, Title: "The Matrix", Monitored: false, HasFile: true, SizeOnDisk: 8_000_000_000, Added: fixedNow.Add(-100 * 24 * time.Hour)},
	}}
	tc := &tautulli.Fake{HistoryRows: []tautulli.HistoryRow{
		// A same-titled episode row must NOT match the movie (media type
		// gates the match, not title alone).
		{MediaType: "episode", GrandparentTitle: "The Matrix", Date: fixedNow},
		{MediaType: "movie", Title: "the matrix", Date: fixedNow.Add(-2 * 24 * time.Hour), PercentComplete: 90},
	}}
	oc := &overseerr.Fake{RequestList: []overseerr.Request{
		{TMDBID: 603, RequestedBy: "bob@example.com"},
	}}

	items, err := Collect(context.Background(), collectDeps(nil, rc, tc, oc))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.EntityKey != "radarr:10" {
		t.Errorf("EntityKey = %q, want radarr:10", it.EntityKey)
	}
	if it.Kind != "movie" {
		t.Errorf("Kind = %q, want movie", it.Kind)
	}
	if !it.WatchedFully {
		t.Errorf("WatchedFully = false, want true (PercentComplete>=85)")
	}
	if it.RequestedBy != "bob@example.com" {
		t.Errorf("RequestedBy = %q, want bob@example.com", it.RequestedBy)
	}
	if !it.Ended {
		t.Errorf("Ended = false, want true (movie with nothing monitored)")
	}
}

func TestCollectRequesterLookupFirstMatchWins(t *testing.T) {
	rc := &radarr.Fake{MovieList: []radarr.Movie{
		{ID: 11, TMDBID: 1, Title: "Dup"},
	}}
	oc := &overseerr.Fake{RequestList: []overseerr.Request{
		{TMDBID: 1, RequestedBy: "first@example.com"},
		{TMDBID: 1, RequestedBy: "second@example.com"},
	}}
	items, err := Collect(context.Background(), collectDeps(nil, rc, &tautulli.Fake{}, oc))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if items[0].RequestedBy != "first@example.com" {
		t.Fatalf("RequestedBy = %q, want first@example.com", items[0].RequestedBy)
	}
}

func TestCollectOverseerrNilLeavesRequestedByEmpty(t *testing.T) {
	sc := &sonarr.Fake{SeriesList: []sonarr.Series{{ID: 1, TVDBID: 100, Title: "Show"}}}
	items, err := Collect(context.Background(), collectDeps(sc, nil, &tautulli.Fake{}, nil))
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if items[0].RequestedBy != "" {
		t.Fatalf("RequestedBy = %q, want empty", items[0].RequestedBy)
	}
}

func TestCollectWrapsClientErrors(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name string
		deps check.Deps
	}{
		{"sonarr series error", collectDeps(&sonarr.Fake{Err: boom}, nil, &tautulli.Fake{}, nil)},
		{"radarr movies error", collectDeps(nil, &radarr.Fake{Err: boom}, &tautulli.Fake{}, nil)},
		{"tautulli history error", collectDeps(&sonarr.Fake{}, nil, &tautulli.Fake{Err: boom}, nil)},
		{"overseerr requests error", collectDeps(&sonarr.Fake{}, nil, &tautulli.Fake{}, &overseerr.Fake{Err: boom})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Collect(context.Background(), tt.deps); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want wrapping %v", err, boom)
			}
		})
	}
}

func TestCollectHistoryWindowCoversTwiceDaysHorizon(t *testing.T) {
	tc := &tautulli.Fake{}
	sc := &sonarr.Fake{}
	if _, err := Collect(context.Background(), collectDeps(sc, nil, tc, nil)); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(tc.Calls) != 1 {
		t.Fatalf("tautulli calls = %v, want exactly one History call", tc.Calls)
	}
	wantSince := fixedNow.Add(-360 * 24 * time.Hour) // 2 * DaysHorizon(180)
	want := "History(" + wantSince.Format(time.RFC3339) + ",5000)"
	if tc.Calls[0] != want {
		t.Fatalf("tautulli call = %q, want %q", tc.Calls[0], want)
	}
}
