// Package radarr is a typed client for the Radarr v3 API.
package radarr

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
	ID                    int64     `json:"id"`
	MovieID               int64     `json:"movieId"`
	Title                 string    `json:"title"`
	Status                string    `json:"status"`
	TrackedDownloadStatus string    `json:"trackedDownloadStatus"`
	TrackedDownloadState  string    `json:"trackedDownloadState"`
	DownloadID            string    `json:"downloadId"`
	OutputPath            string    `json:"outputPath"`
	Size                  float64   `json:"size"`
	SizeLeft              float64   `json:"sizeLeft"`
	Messages              []string  `json:"messages"`
	Added                 time.Time `json:"added"`
}

type Movie struct {
	ID         int64     `json:"id"`
	TMDBID     int64     `json:"tmdbId"`
	Title      string    `json:"title"`
	Year       int       `json:"year"`
	Monitored  bool      `json:"monitored"`
	HasFile    bool      `json:"hasFile"`
	Status     string    `json:"status"`
	Path       string    `json:"path"`
	Added      time.Time `json:"added"`
	SizeOnDisk int64     `json:"sizeOnDisk"`
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
	MovieID     int64     `json:"movieId"`
	DownloadID  string    `json:"downloadId"`
}

// Client is the behaviour the rest of healarr depends on.
type Client interface {
	Health(ctx context.Context) ([]HealthItem, error)
	Queue(ctx context.Context) ([]QueueItem, error)
	Movies(ctx context.Context) ([]Movie, error)
	RootFolders(ctx context.Context) ([]RootFolder, error)
	WantedMissingCount(ctx context.Context) (int, error)
	History(ctx context.Context, since time.Time, eventType string) ([]HistoryRecord, error)
	DeleteQueueItem(ctx context.Context, id int64, removeFromClient, blocklist bool) error
	UpdateMovieMonitored(ctx context.Context, id int64, monitored bool) error
	DeleteMovie(ctx context.Context, id int64, deleteFiles, addExclusion bool) error
	RunCommand(ctx context.Context, name string, params map[string]any) (int64, error)
}
