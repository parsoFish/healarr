// Package staleness implements ADR-016's deterministic staleness score
// (spec C6): a pure scorer (ScoreItem, Band) over an Item gathered from
// Sonarr/Radarr, Tautulli and Overseerr by Collect, plus the
// staleness_scan check that turns scored items into findings.
package staleness

import (
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// Item is one series or movie candidate for staleness scoring, as
// assembled by Collect from Sonarr/Radarr, Tautulli and Overseerr. Every
// field ScoreItem reads is already resolved here — the scorer itself
// never talks to a client.
type Item struct {
	EntityKey string // "sonarr:<id>" or "radarr:<id>"
	Title     string
	Kind      string // "series" | "movie"

	LastWatched      time.Time // zero if never watched
	WatchedFully     bool
	WatchedPartially bool

	// Ended is the C6 "arr status" condition: for a series, Status ==
	// "ended"; for a movie, nothing monitored (Collect sets it to
	// !Monitored for movies, since Radarr has no "ended" status).
	Ended     bool
	Monitored bool

	SizeBytes   int64
	RequestedBy string // Overseerr requester, "" if unknown/no request
	Added       time.Time
}

// Score is ScoreItem's result: a Total in [0,100] plus each C6
// component's own contribution, keyed by "days", "completion", "arr",
// "size", "requester", "age".
type Score struct {
	Total      float64
	Components map[string]float64
}

// Band names for Score.Total against config.Staleness's thresholds.
const (
	BandCandidate  = "candidate"
	BandWatchlist  = "watchlist"
	BandSuppressed = "suppressed"
)

// ScoreItem computes it's C6 staleness score against weights w as of now.
// It is pure: it never mutates it (or w), and always returns the same
// Score for the same inputs. Total is the sum of every component, clamped
// to [0,100] — an individual component (e.g. ContinuingPoints) may be
// negative, but the total the caller sees never is.
func ScoreItem(it Item, w config.Staleness, now time.Time) Score {
	c := map[string]float64{
		"days":       daysScore(it, w, now),
		"completion": completionScore(it, w),
		"arr":        arrScore(it, w),
		"size":       sizeScore(it, w),
		"requester":  requesterScore(it, w),
		"age":        ageScore(it, w, now),
	}
	total := c["days"] + c["completion"] + c["arr"] + c["size"] + c["requester"] + c["age"]
	return Score{Total: clamp(total, 0, 100), Components: c}
}

// Band classifies total against w's thresholds: "candidate"
// (>= CandidateThreshold), "watchlist" (>= WatchlistThreshold), or
// "suppressed" otherwise.
func Band(total float64, w config.Staleness) string {
	switch {
	case total >= w.CandidateThreshold:
		return BandCandidate
	case total >= w.WatchlistThreshold:
		return BandWatchlist
	default:
		return BandSuppressed
	}
}

func clamp(v, lo, hi float64) float64 {
	switch {
	case v < lo:
		return lo
	case v > hi:
		return hi
	default:
		return v
	}
}

// daysScore is C6's "days since last watch" component: linear 0 ->
// DaysMaxPoints over 0..DaysHorizon days since LastWatched; if never
// watched, full DaysMaxPoints once Added is more than
// NeverWatchedAfterDays in the past, otherwise 0 (recently added, hasn't
// had a chance to be watched yet). DaysHorizon<=0 is treated as 1 to
// guard the division; NeverWatchedAfterDays<=0 disables the never-watched
// rule (returns 0) rather than awarding every never-watched item full
// points off an unconfigured threshold.
func daysScore(it Item, w config.Staleness, now time.Time) float64 {
	if it.LastWatched.IsZero() {
		return neverWatchedDaysScore(it, w, now)
	}
	horizon := w.DaysHorizon
	if horizon <= 0 {
		horizon = 1
	}
	daysSince := now.Sub(it.LastWatched).Hours() / 24
	if daysSince < 0 {
		daysSince = 0
	}
	frac := daysSince / float64(horizon)
	if frac > 1 {
		frac = 1
	}
	return frac * w.DaysMaxPoints
}

func neverWatchedDaysScore(it Item, w config.Staleness, now time.Time) float64 {
	if w.NeverWatchedAfterDays <= 0 || it.Added.IsZero() {
		return 0
	}
	ageDays := now.Sub(it.Added).Hours() / 24
	if ageDays > float64(w.NeverWatchedAfterDays) {
		return w.DaysMaxPoints
	}
	return 0
}

// completionScore is C6's watch-completion component: fully watched ->
// WatchedFullPoints, partially watched -> WatchedPartialPoints, neither
// -> 0.
func completionScore(it Item, w config.Staleness) float64 {
	switch {
	case it.WatchedFully:
		return w.WatchedFullPoints
	case it.WatchedPartially:
		return w.WatchedPartialPoints
	default:
		return 0
	}
}

// arrScore is C6's arr-status component: Ended (series with Status ==
// "ended", or a movie with nothing monitored — Collect resolves both to
// this one field) -> EndedPoints; otherwise Monitored (continuing &
// monitored) -> ContinuingPoints; otherwise 0.
func arrScore(it Item, w config.Staleness) float64 {
	switch {
	case it.Ended:
		return w.EndedPoints
	case it.Monitored:
		return w.ContinuingPoints
	default:
		return 0
	}
}

// sizeScore is C6's size component: SizeBytes converted to GB times
// SizePointsPerGB, capped at SizeMaxPoints.
func sizeScore(it Item, w config.Staleness) float64 {
	gb := float64(it.SizeBytes) / 1e9
	return clamp(gb*w.SizePointsPerGB, 0, w.SizeMaxPoints)
}

// requesterScore is C6's Overseerr-requester component: unknown requester
// -> 0; the configured owner -> OwnerRequesterPoints (regardless of watch
// status); someone else who hasn't watched it -> OtherRequesterPoints;
// someone else who has watched it -> 0.
func requesterScore(it Item, w config.Staleness) float64 {
	switch {
	case it.RequestedBy == "":
		return 0
	case it.RequestedBy == w.Owner:
		return w.OwnerRequesterPoints
	case it.WatchedFully || it.WatchedPartially:
		return 0
	default:
		return w.OtherRequesterPoints
	}
}

// ageScore is C6's age-since-added component: days since Added times
// AgePointsPerDay, capped at AgeMaxPoints. An unset Added is 0, not an
// error — Collect always sets it from Sonarr/Radarr's own Added field,
// but a zero value must still score cleanly rather than producing a huge
// spurious duration.
func ageScore(it Item, w config.Staleness, now time.Time) float64 {
	if it.Added.IsZero() {
		return 0
	}
	days := now.Sub(it.Added).Hours() / 24
	if days < 0 {
		days = 0
	}
	return clamp(days*w.AgePointsPerDay, 0, w.AgeMaxPoints)
}
