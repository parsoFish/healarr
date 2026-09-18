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

// looseScalarText extracts the literal text of a JSON scalar exactly as
// Go's json package hands it to UnmarshalJSON: a bare literal (a number,
// or another bare token) or a quoted string. Tautulli's get_history sends
// several numeric-looking fields inconsistently — a bare number on most
// rows, but "" (and occasionally a quoted numeric string) on others, e.g.
// grandparent_rating_key/parent_rating_key on movie rows, which have no
// parent hierarchy. ok is false for JSON null, letting callers substitute
// a zero value the same way they do for "". A non-nil err means data was a
// quoted value that could not be unquoted.
func looseScalarText(data []byte) (text string, ok bool, err error) {
	if string(data) == "null" {
		return "", false, nil
	}
	if len(data) >= 2 && data[0] == '"' {
		s, uerr := strconv.Unquote(string(data))
		if uerr != nil {
			return "", false, fmt.Errorf("tautulli: decode quoted value %q: %w", data, uerr)
		}
		return s, true, nil
	}
	return string(data), true, nil
}

// looseNumberString decodes a Tautulli id-like history field (e.g.
// rating_key, parent_rating_key) that may arrive as a JSON number, a
// numeric string, an empty string, or null. It behaves like json.Number
// for the numeric and null cases, but additionally tolerates an empty
// quoted string ("" -> ""), which json.Number rejects with "invalid number
// literal".
type looseNumberString string

// String reports l's literal text, matching json.Number's String method
// so callers can switch between the two without changing call sites.
func (l looseNumberString) String() string { return string(l) }

func (l *looseNumberString) UnmarshalJSON(data []byte) error {
	text, ok, err := looseScalarText(data)
	if err != nil {
		return err
	}
	if !ok {
		*l = ""
		return nil
	}
	*l = looseNumberString(text)
	return nil
}

// looseFloat decodes a Tautulli numeric history field (e.g.
// watched_status) that may arrive as a JSON number, a numeric string, an
// empty string, or null, mapping ""/null to zero instead of failing the
// whole history decode.
type looseFloat float64

func (l *looseFloat) UnmarshalJSON(data []byte) error {
	text, ok, err := looseScalarText(data)
	if err != nil {
		return err
	}
	if !ok || text == "" {
		*l = 0
		return nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return fmt.Errorf("tautulli: decode number %q: %w", data, err)
	}
	*l = looseFloat(f)
	return nil
}

// looseInt is looseFloat's integer counterpart, for fields like
// percent_complete.
type looseInt int

func (l *looseInt) UnmarshalJSON(data []byte) error {
	text, ok, err := looseScalarText(data)
	if err != nil {
		return err
	}
	if !ok || text == "" {
		*l = 0
		return nil
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("tautulli: decode integer %q: %w", data, err)
	}
	*l = looseInt(n)
	return nil
}

// epochTime decodes a Tautulli epoch-seconds timestamp field (e.g. "date")
// as the zero time.Time when it is zero, negative, "", or null; it also
// tolerates a quoted numeric string in addition to the usual bare integer.
type epochTime struct{ time.Time }

func (e *epochTime) UnmarshalJSON(data []byte) error {
	text, ok, err := looseScalarText(data)
	if err != nil {
		return err
	}
	if !ok || text == "" {
		e.Time = time.Time{}
		return nil
	}
	sec, err := strconv.ParseInt(text, 10, 64)
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
