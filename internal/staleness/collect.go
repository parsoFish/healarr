package staleness

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/clients/tautulli"
)

// tautulliHistoryLength bounds how many rows Collect asks Tautulli for in
// one call; large enough to cover a busy library's watch history within
// the lookback window (2x Staleness.DaysHorizon) without paging.
const tautulliHistoryLength = 5000

// watchFullThreshold is the Tautulli percent_complete value at or above
// which a row counts as a full watch even when watched_status hasn't been
// set to 1 (some clients report playback progress without ever flipping
// the watched flag, e.g. a rewatch that stops just short of the credits).
const watchFullThreshold = 85

const (
	tautulliEpisodeMediaType = "episode"
	tautulliMovieMediaType   = "movie"
)

// Collect gathers every Sonarr series and Radarr movie, joins each one
// against Tautulli watch history and (when configured) Overseerr request
// data, and returns one Item per title. It requires at least one of
// Sonarr/Radarr plus Tautulli (check.ErrNotConfigured otherwise);
// Overseerr is optional, and a nil d.Overseerr simply leaves every
// Item.RequestedBy empty.
//
// Tautulli history rows carry no Sonarr/Radarr id, so matching is by
// title: an episode row's GrandparentTitle (the show it belongs to) is
// compared case-insensitively against the series title, and a movie
// row's Title is compared case-insensitively against the movie title
// (each additionally gated on the row's MediaType, so an episode can
// never match a movie of the same name or vice versa). This is a
// heuristic, not an exact join — a re-titled or differently-punctuated
// entry on either side won't match — but it is the only signal
// Tautulli's history API offers without a rating-key crosswalk this
// phase doesn't build.
func Collect(ctx context.Context, d check.Deps) ([]Item, error) {
	if d.Sonarr == nil && d.Radarr == nil {
		return nil, check.ErrNotConfigured
	}
	if d.Tautulli == nil {
		return nil, check.ErrNotConfigured
	}

	rows, err := d.Tautulli.History(ctx, historyWindow(d), tautulliHistoryLength)
	if err != nil {
		return nil, fmt.Errorf("staleness: tautulli history: %w", err)
	}
	requests, err := overseerrRequests(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("staleness: overseerr requests: %w", err)
	}
	tvdbRequester, tmdbRequester := requesterLookups(requests)

	var items []Item
	if d.Sonarr != nil {
		series, err := d.Sonarr.Series(ctx)
		if err != nil {
			return nil, fmt.Errorf("staleness: sonarr series: %w", err)
		}
		for _, s := range series {
			items = append(items, seriesItem(s, rows, tvdbRequester))
		}
	}
	if d.Radarr != nil {
		movies, err := d.Radarr.Movies(ctx)
		if err != nil {
			return nil, fmt.Errorf("staleness: radarr movies: %w", err)
		}
		for _, m := range movies {
			items = append(items, movieItem(m, rows, tmdbRequester))
		}
	}
	return items, nil
}

// historyWindow is how far back Collect asks Tautulli to look: twice
// Staleness.DaysHorizon, wide enough that a watch just outside the
// horizon still resolves LastWatched correctly rather than looking
// never-watched. DaysHorizon<=0 is treated as 1, mirroring ScoreItem's
// own guard.
func historyWindow(d check.Deps) time.Time {
	horizon := d.Cfg.Staleness.DaysHorizon
	if horizon <= 0 {
		horizon = 1
	}
	return d.Now().Add(-2 * time.Duration(horizon) * 24 * time.Hour)
}

func overseerrRequests(ctx context.Context, d check.Deps) ([]overseerr.Request, error) {
	if d.Overseerr == nil {
		return nil, nil
	}
	return d.Overseerr.Requests(ctx, "all")
}

// requesterLookups builds id->requester maps from reqs: tvdb for series
// (TVDBID != 0), tmdb for movies (TMDBID != 0). The first request seen
// for a given id wins, so results are deterministic given the input
// order rather than depending on which duplicate request happened to
// come last.
func requesterLookups(reqs []overseerr.Request) (tvdb, tmdb map[int64]string) {
	tvdb = map[int64]string{}
	tmdb = map[int64]string{}
	for _, r := range reqs {
		if r.TVDBID != 0 {
			if _, ok := tvdb[r.TVDBID]; !ok {
				tvdb[r.TVDBID] = r.RequestedBy
			}
		}
		if r.TMDBID != 0 {
			if _, ok := tmdb[r.TMDBID]; !ok {
				tmdb[r.TMDBID] = r.RequestedBy
			}
		}
	}
	return tvdb, tmdb
}

func seriesItem(s sonarr.Series, rows []tautulli.HistoryRow, tvdbRequester map[int64]string) Item {
	last, fully, partially := watchSummary(matchRows(rows, tautulliEpisodeMediaType, s.Title, true))
	return Item{
		EntityKey:        fmt.Sprintf("sonarr:%d", s.ID),
		Title:            s.Title,
		Kind:             "series",
		LastWatched:      last,
		WatchedFully:     fully,
		WatchedPartially: partially,
		Ended:            strings.EqualFold(s.Status, "ended"),
		Monitored:        s.Monitored,
		SizeBytes:        s.SizeOnDisk,
		RequestedBy:      tvdbRequester[s.TVDBID],
		Added:            s.Added,
	}
}

func movieItem(m radarr.Movie, rows []tautulli.HistoryRow, tmdbRequester map[int64]string) Item {
	last, fully, partially := watchSummary(matchRows(rows, tautulliMovieMediaType, m.Title, false))
	return Item{
		EntityKey:        fmt.Sprintf("radarr:%d", m.ID),
		Title:            m.Title,
		Kind:             "movie",
		LastWatched:      last,
		WatchedFully:     fully,
		WatchedPartially: partially,
		Ended:            !m.Monitored, // Radarr has no "ended" status; "nothing monitored" is the C6 analogue.
		Monitored:        m.Monitored,
		SizeBytes:        m.SizeOnDisk,
		RequestedBy:      tmdbRequester[m.TMDBID],
		Added:            m.Added,
	}
}

// matchRows filters rows to those of mediaType whose title field
// (GrandparentTitle for an episode, Title for a movie) case-insensitively
// equals title.
func matchRows(rows []tautulli.HistoryRow, mediaType, title string, episode bool) []tautulli.HistoryRow {
	var out []tautulli.HistoryRow
	for _, r := range rows {
		if r.MediaType != mediaType {
			continue
		}
		rowTitle := r.Title
		if episode {
			rowTitle = r.GrandparentTitle
		}
		if strings.EqualFold(rowTitle, title) {
			out = append(out, r)
		}
	}
	return out
}

// watchSummary reduces matched history rows to the three fields ScoreItem
// needs: the most recent watch time, whether any row was a full watch
// (watched_status>=1 or percent_complete>=watchFullThreshold), and
// (only when none were) whether at least one row exists at all.
func watchSummary(rows []tautulli.HistoryRow) (last time.Time, fully, partially bool) {
	for _, r := range rows {
		if r.Date.After(last) {
			last = r.Date
		}
		if r.WatchedStatus >= 1 || r.PercentComplete >= watchFullThreshold {
			fully = true
		}
	}
	partially = len(rows) > 0 && !fully
	return last, fully, partially
}
