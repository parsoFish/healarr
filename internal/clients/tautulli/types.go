// Package tautulli is a typed client for the Tautulli API.
package tautulli

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// HistoryRow is one playback session from Tautulli's watch history.
type HistoryRow struct {
	RatingKey            string    `json:"ratingKey"`
	GrandparentRatingKey string    `json:"grandparentRatingKey"`
	ParentRatingKey      string    `json:"parentRatingKey"`
	Title                string    `json:"title"`
	GrandparentTitle     string    `json:"grandparentTitle"`
	MediaType            string    `json:"mediaType"`
	User                 string    `json:"user"`
	Date                 time.Time `json:"date"`
	WatchedStatus        float64   `json:"watchedStatus"`
	PercentComplete      int       `json:"percentComplete"`
}

// Client is the behaviour the rest of healarr depends on.
type Client interface {
	Ping(ctx context.Context) error
	History(ctx context.Context, since time.Time, length int) ([]HistoryRow, error)
	ActivityCount(ctx context.Context) (int, error)
}

// epochTime decodes a Tautulli epoch-seconds timestamp field (a bare JSON
// integer, e.g. "date") as the zero time.Time when it is zero or negative.
type epochTime struct{ time.Time }

func (e *epochTime) UnmarshalJSON(data []byte) error {
	sec, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("tautulli: decode epoch time %q: %w", data, err)
	}
	if sec <= 0 {
		e.Time = time.Time{}
		return nil
	}
	e.Time = time.Unix(sec, 0).UTC()
	return nil
}
