// Package overseerr is a typed client for the Overseerr API.
package overseerr

import (
	"context"
	"time"
)

// Status is Overseerr's server status combined with its public settings.
type Status struct {
	Version     string `json:"version"`
	Initialized bool   `json:"initialized"`
}

// Request is a single media request.
type Request struct {
	ID          int64     `json:"id"`
	Status      int       `json:"status"`
	MediaType   string    `json:"mediaType"`
	TMDBID      int64     `json:"tmdbId"`
	TVDBID      int64     `json:"tvdbId"`
	MediaStatus int       `json:"mediaStatus"`
	RequestedBy string    `json:"requestedBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Client is the behaviour the rest of healarr depends on.
type Client interface {
	Status(ctx context.Context) (Status, error)
	Requests(ctx context.Context, filter string) ([]Request, error)
	DeclineRequest(ctx context.Context, id int64) error
}
