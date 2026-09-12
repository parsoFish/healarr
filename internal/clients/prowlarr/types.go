// Package prowlarr is a typed client for the Prowlarr v1 API.
package prowlarr

import (
	"bytes"
	"context"
	"time"
)

type HealthItem struct {
	Type    string `json:"type"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

type Indexer struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Enable   bool   `json:"enable"`
	Protocol string `json:"protocol"`
	Priority int    `json:"priority"`
}

type IndexerStatus struct {
	IndexerID         int64
	MostRecentFailure time.Time
	DisabledTill      time.Time
}

// nullTime decodes a Prowlarr timestamp field that may be JSON null or the
// empty string as the zero time.Time, instead of failing to parse.
type nullTime struct{ time.Time }

func (n *nullTime) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte(`""`)) {
		n.Time = time.Time{}
		return nil
	}
	return n.Time.UnmarshalJSON(data)
}

// Client is the behaviour the rest of healarr depends on.
type Client interface {
	Health(ctx context.Context) ([]HealthItem, error)
	Indexers(ctx context.Context) ([]Indexer, error)
	IndexerStatus(ctx context.Context) ([]IndexerStatus, error)
	DeleteIndexer(ctx context.Context, id int64) error
	TestIndexer(ctx context.Context, id int64) error
}
