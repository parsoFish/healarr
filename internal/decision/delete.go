package decision

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
)

// executeDelete is Execute's "delete" branch: gated behind
// cfg.Actions.Enabled, it removes the entity from Sonarr/Radarr (with
// files and an import-list exclusion, ADR-016), then best-effort
// declines any matching Overseerr request and tells the peer which
// torrents to remove — neither of those can fail the decision itself.
func executeDelete(ctx context.Context, d Deps, dec store.Decision) (store.Decision, error) {
	now := d.Now()
	if !d.Cfg.Actions.Enabled {
		return blockDelete(ctx, d, dec, now)
	}

	ref, err := parseEntityKey(dec.EntityKey)
	if err != nil {
		return failDelete(ctx, d, dec, now, err)
	}

	switch ref.Provider {
	case "sonarr":
		return executeDeleteSonarr(ctx, d, dec, ref, now)
	default: // "radarr" — parseEntityKey allows no other provider
		return executeDeleteRadarr(ctx, d, dec, ref, now)
	}
}

// executeDeleteSonarr deletes ref.ID from Sonarr, then best-effort
// declines matching Overseerr requests and hints the peer.
func executeDeleteSonarr(ctx context.Context, d Deps, dec store.Decision, ref entityRef, now time.Time) (store.Decision, error) {
	if d.Sonarr == nil {
		return failDelete(ctx, d, dec, now, fmt.Errorf("%w: sonarr", check.ErrNotConfigured))
	}

	tvdbID, title, lookupNote := lookupSonarrSeries(ctx, d, ref.ID)

	if err := d.Sonarr.DeleteSeries(ctx, ref.ID, true, true); err != nil {
		return failDelete(ctx, d, dec, now, fmt.Errorf("delete series %d: %w", ref.ID, err))
	}

	parts := []string{fmt.Sprintf("deleted sonarr series %d (%s)", ref.ID, title)}
	parts = append(parts, lookupNote...)
	parts = append(parts, declineOverseerrRequests(ctx, d, "tv", tvdbID)...)
	parts = append(parts, sendQbitDeleteHint(ctx, d, dec.EntityKey, sonarrDownloadHashes(ctx, d, ref.ID, now))...)

	return finishDelete(ctx, d, dec, now, parts)
}

// executeDeleteRadarr deletes ref.ID from Radarr, then best-effort
// declines matching Overseerr requests and hints the peer.
func executeDeleteRadarr(ctx context.Context, d Deps, dec store.Decision, ref entityRef, now time.Time) (store.Decision, error) {
	if d.Radarr == nil {
		return failDelete(ctx, d, dec, now, fmt.Errorf("%w: radarr", check.ErrNotConfigured))
	}

	tmdbID, title, lookupNote := lookupRadarrMovie(ctx, d, ref.ID)

	if err := d.Radarr.DeleteMovie(ctx, ref.ID, true, true); err != nil {
		return failDelete(ctx, d, dec, now, fmt.Errorf("delete movie %d: %w", ref.ID, err))
	}

	parts := []string{fmt.Sprintf("deleted radarr movie %d (%s)", ref.ID, title)}
	parts = append(parts, lookupNote...)
	parts = append(parts, declineOverseerrRequests(ctx, d, "movie", tmdbID)...)
	parts = append(parts, sendQbitDeleteHint(ctx, d, dec.EntityKey, radarrDownloadHashes(ctx, d, ref.ID, now))...)

	return finishDelete(ctx, d, dec, now, parts)
}

// finishDelete marks dec executed and records an executed remediation
// whose Detail joins every step's outcome (constraints.md: best-effort
// failures are logged and recorded in the remediation Detail, never
// swallowed).
func finishDelete(ctx context.Context, d Deps, dec store.Decision, now time.Time, parts []string) (store.Decision, error) {
	recordDeleteRemediation(ctx, d, dec.ID, "executed", now, strings.Join(parts, "; "))
	if err := d.Store.MarkDecision(ctx, dec.ID, "executed", now, nil); err != nil {
		return store.Decision{}, fmt.Errorf("decision: delete %d: mark executed: %w", dec.ID, err)
	}
	dec.Status = "executed"
	dec.ExecutedAt = &now
	return dec, nil
}

// lookupSonarrSeries best-effort looks up id's TVDB id and title before
// it is deleted (DeleteSeries removes it from Sonarr's own index, so
// this must happen first). A lookup failure — or id simply not being
// found — only means the Overseerr decline step below has nothing to
// match on; it never blocks the delete itself, but (like every other
// best-effort step) the failure is still logged and returned as a note
// for the remediation Detail, never silently dropped.
func lookupSonarrSeries(ctx context.Context, d Deps, id int64) (tvdbID int64, title string, note []string) {
	all, err := d.Sonarr.Series(ctx)
	if err != nil {
		slog.Default().Warn("decision: sonarr series lookup failed before delete", "id", id, "error", err)
		return 0, "", []string{fmt.Sprintf("sonarr lookup: %v", err)}
	}
	for _, s := range all {
		if s.ID == id {
			return s.TVDBID, s.Title, nil
		}
	}
	return 0, "", nil
}

// lookupRadarrMovie is lookupSonarrSeries's Radarr equivalent.
func lookupRadarrMovie(ctx context.Context, d Deps, id int64) (tmdbID int64, title string, note []string) {
	all, err := d.Radarr.Movies(ctx)
	if err != nil {
		slog.Default().Warn("decision: radarr movies lookup failed before delete", "id", id, "error", err)
		return 0, "", []string{fmt.Sprintf("radarr lookup: %v", err)}
	}
	for _, m := range all {
		if m.ID == id {
			return m.TMDBID, m.Title, nil
		}
	}
	return 0, "", nil
}

// declineOverseerrRequests best-effort declines every request matching
// mediaType ("tv"|"movie") and extID (the TVDB/TMDB id looked up before
// deleting). extID == 0 (lookup failed, or nothing to match) and no
// Overseerr client configured both skip this entirely — there being
// nothing to decline is not an error. Every outcome (including failures)
// is returned as a human-readable line for the remediation Detail.
func declineOverseerrRequests(ctx context.Context, d Deps, mediaType string, extID int64) []string {
	if d.Overseerr == nil || extID == 0 {
		return nil
	}

	reqs, err := d.Overseerr.Requests(ctx, "")
	if err != nil {
		slog.Default().Warn("decision: overseerr requests lookup failed", "error", err)
		return []string{fmt.Sprintf("overseerr requests: %v", err)}
	}

	var declined int
	var lines []string
	for _, r := range reqs {
		if r.MediaType != mediaType {
			continue
		}
		var match bool
		switch mediaType {
		case "tv":
			match = r.TVDBID == extID
		case "movie":
			match = r.TMDBID == extID
		}
		if !match {
			continue
		}
		if err := d.Overseerr.DeclineRequest(ctx, r.ID); err != nil {
			slog.Default().Warn("decision: decline overseerr request failed", "request_id", r.ID, "error", err)
			lines = append(lines, fmt.Sprintf("decline overseerr request %d: %v", r.ID, err))
			continue
		}
		declined++
	}
	return append([]string{fmt.Sprintf("declined %d overseerr request(s)", declined)}, lines...)
}

// sendQbitDeleteHint best-effort tells the peer which torrents to remove
// too. It is a no-op when there is no peer configured or no known
// download ids (the peer send is only ever a hint, never load-bearing).
func sendQbitDeleteHint(ctx context.Context, d Deps, entityKey string, hashes []string) []string {
	if d.Peer == nil || len(hashes) == 0 {
		return nil
	}
	_, err := d.Peer.SendDecision(ctx, peer.Decision{
		Kind:        "qbit_delete",
		EntityKey:   entityKey,
		Payload:     map[string]any{"hashes": hashes},
		RequestedAt: d.Now(),
	})
	if err != nil {
		slog.Default().Warn("decision: send qbit_delete hint to peer failed", "entity_key", entityKey, "error", err)
		return []string{fmt.Sprintf("qbit_delete peer send: %v", err)}
	}
	return []string{fmt.Sprintf("sent qbit_delete hint for %d torrent(s)", len(hashes))}
}

// sonarrDownloadHashes best-effort collects the unique, upper-cased
// download ids from seriesID's Sonarr history within historyLookback. A
// lookup failure means the peer simply isn't sent a hint; it never fails
// the delete.
func sonarrDownloadHashes(ctx context.Context, d Deps, seriesID int64, now time.Time) []string {
	recs, err := d.Sonarr.History(ctx, now.Add(-historyLookback), "")
	if err != nil {
		slog.Default().Warn("decision: sonarr history lookup failed", "series_id", seriesID, "error", err)
		return nil
	}
	seen := map[string]struct{}{}
	var hashes []string
	for _, r := range recs {
		if r.SeriesID != seriesID || r.DownloadID == "" {
			continue
		}
		h := strings.ToUpper(r.DownloadID)
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		hashes = append(hashes, h)
	}
	return hashes
}

// radarrDownloadHashes is sonarrDownloadHashes's Radarr equivalent.
func radarrDownloadHashes(ctx context.Context, d Deps, movieID int64, now time.Time) []string {
	recs, err := d.Radarr.History(ctx, now.Add(-historyLookback), "")
	if err != nil {
		slog.Default().Warn("decision: radarr history lookup failed", "movie_id", movieID, "error", err)
		return nil
	}
	seen := map[string]struct{}{}
	var hashes []string
	for _, r := range recs {
		if r.MovieID != movieID || r.DownloadID == "" {
			continue
		}
		h := strings.ToUpper(r.DownloadID)
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		hashes = append(hashes, h)
	}
	return hashes
}
