// Package sonarr is a typed client for the Sonarr v3 API.
package sonarr

import (
	"context"
	"time"
)

type HealthItem struct {
	Type    string `json:"type"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

type QueueItem struct {
	ID                    int64
	SeriesID              int64
	EpisodeID             int64
	Title                 string
	Status                string
	TrackedDownloadStatus string
	TrackedDownloadState  string
	DownloadID            string
	OutputPath            string
	Size                  float64
	SizeLeft              float64
	Messages              []string
	Added                 time.Time
}

type Series struct {
	ID               int64
	TVDBID           int64
	Title            string
	Monitored        bool
	Status           string
	Path             string
	Added            time.Time
	EpisodeFileCount int
	EpisodeCount     int
	SizeOnDisk       int64
}

type RootFolder struct {
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	FreeSpace  int64  `json:"freeSpace"`
}

type HistoryRecord struct {
	ID          int64     `json:"id"`
	Date        time.Time `json:"date"`
	EventType   string    `json:"eventType"`
	SourceTitle string    `json:"sourceTitle"`
	SeriesID    int64     `json:"seriesId"`
	EpisodeID   int64     `json:"episodeId"`
	DownloadID  string    `json:"downloadId"`
}

// Client is the behaviour the rest of healarr depends on.
type Client interface {
	Health(ctx context.Context) ([]HealthItem, error)
	Queue(ctx context.Context) ([]QueueItem, error)
	Series(ctx context.Context) ([]Series, error)
	RootFolders(ctx context.Context) ([]RootFolder, error)
	WantedMissingCount(ctx context.Context) (int, error)
	History(ctx context.Context, since time.Time, eventType string) ([]HistoryRecord, error)
	DeleteQueueItem(ctx context.Context, id int64, removeFromClient, blocklist bool) error
	UpdateSeriesMonitored(ctx context.Context, id int64, monitored bool) error
	DeleteSeries(ctx context.Context, id int64, deleteFiles, addExclusion bool) error
	RunCommand(ctx context.Context, name string, params map[string]any) (int64, error)
}
