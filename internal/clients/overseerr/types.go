// Package overseerr is a typed client for the Overseerr API.
package overseerr

import (
	"context"
	"time"
)

// Status is Overseerr's server status combined with its public settings.
type Status struct {
	Version     string
	Initialized bool
}

// Request is a single media request.
type Request struct {
	ID          int64
	Status      int
	MediaType   string
	TMDBID      int64
	TVDBID      int64
	MediaStatus int
	RequestedBy string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Client is the behaviour the rest of healarr depends on.
type Client interface {
	Status(ctx context.Context) (Status, error)
	Requests(ctx context.Context, filter string) ([]Request, error)
	DeclineRequest(ctx context.Context, id int64) error
}
