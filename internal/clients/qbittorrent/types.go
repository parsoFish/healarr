// Package qbittorrent is a typed client for the qBittorrent WebUI v2 API.
package qbittorrent

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Torrent is a single entry from /api/v2/torrents/info.
type Torrent struct {
	Hash, Name, State, Category, SavePath, ContentPath string
	Progress, Ratio                                    float64
	Size, Completed, Downloaded, Uploaded              int64
	AddedOn, CompletionOn                              time.Time
	SeedingTime                                        time.Duration
	DlSpeed, UpSpeed                                   int64
	NumSeeds, NumLeechs                                int
}

// File is a single entry from /api/v2/torrents/files.
type File struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Progress float64 `json:"progress"`
	Priority int     `json:"priority"`
}

// Preferences is the subset of /api/v2/app/preferences healarr cares about.
type Preferences struct {
	SavePath          string  `json:"save_path"`
	TempPath          string  `json:"temp_path"`
	TempPathEnabled   bool    `json:"temp_path_enabled"`
	MaxRatio          float64 `json:"max_ratio"`
	MaxSeedingTime    int     `json:"max_seeding_time"`
	ExcludedFileNames string  `json:"excluded_file_names"`
}

// Client is the behaviour the rest of healarr depends on.
type Client interface {
	// Version fetches /api/v2/app/version; it doubles as a liveness+auth check.
	Version(ctx context.Context) (string, error)
	// Torrents lists torrents. filter == "" means all; category == "" means
	// every category (qBittorrent filter values include "stalled",
	// "completed", "errored").
	Torrents(ctx context.Context, filter, category string) ([]Torrent, error)
	Files(ctx context.Context, hash string) ([]File, error)
	Delete(ctx context.Context, hashes []string, deleteFiles bool) error
	Reannounce(ctx context.Context, hashes []string) error
	Resume(ctx context.Context, hashes []string) error
	Preferences(ctx context.Context) (Preferences, error)
	SetPreferences(ctx context.Context, patch map[string]any) error
}

// epochTime decodes a qBittorrent epoch-seconds timestamp field. qBittorrent
// uses 0 or -1 to mean "unset"/"never", which this decodes as the zero
// time.Time rather than a spurious 1970 or negative date.
type epochTime struct{ time.Time }

func (e *epochTime) UnmarshalJSON(data []byte) error {
	sec, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("qbittorrent: decode epoch time %q: %w", data, err)
	}
	if sec <= 0 {
		e.Time = time.Time{}
		return nil
	}
	e.Time = time.Unix(sec, 0).UTC()
	return nil
}
