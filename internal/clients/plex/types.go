// Package plex is a typed client for the Plex Media Server API.
package plex

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Identity is Plex's server identity, available without authentication.
type Identity struct {
	MachineIdentifier string
	Version           string
}

// Library is a Plex library section (e.g. "Movies", "TV Shows").
type Library struct {
	Key       string
	Title     string
	Type      string
	ScannedAt time.Time
	UpdatedAt time.Time
}

// Item is a piece of media returned by a library's recently-added listing.
type Item struct {
	RatingKey  string
	Title      string
	Type       string
	AddedAt    time.Time
	LibraryKey string
}

// Client is the behaviour the rest of healarr depends on.
type Client interface {
	Identity(ctx context.Context) (Identity, error)
	Libraries(ctx context.Context) ([]Library, error)
	RecentlyAdded(ctx context.Context, libraryKey string, limit int) ([]Item, error)
	RefreshLibrary(ctx context.Context, libraryKey string) error
}

// epochTime decodes a Plex epoch-seconds timestamp field (a bare JSON
// integer, e.g. scannedAt/updatedAt/addedAt) as the zero time.Time when it is
// zero or negative, instead of a spurious 1970 date.
type epochTime struct{ time.Time }

func (e *epochTime) UnmarshalJSON(data []byte) error {
	sec, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("plex: decode epoch time %q: %w", data, err)
	}
	if sec <= 0 {
		e.Time = time.Time{}
		return nil
	}
	e.Time = time.Unix(sec, 0).UTC()
	return nil
}
