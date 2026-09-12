// Package docker is a client for the Docker Engine API over a unix socket,
// used to inspect and manage the *arr stack's containers on the NAS.
package docker

import (
	"context"
	"time"
)

// Container is one running or stopped container, merging the summary list
// (/containers/json) with per-container inspect data (health, restarts,
// start time).
type Container struct {
	ID           string
	Name         string
	Image        string
	State        string
	Status       string
	Health       string
	RestartCount int
	StartedAt    time.Time
}

// DiskUsage summarises image (and build cache) disk usage as reported by
// GET /system/df.
type DiskUsage struct {
	ImagesTotal       int
	ImagesActive      int
	ImagesSize        int64
	ImagesReclaimable int64
	BuildCacheSize    int64
}

// Client is the behaviour the rest of healarr depends on for Docker.
type Client interface {
	// Containers lists all containers (running and stopped), enriched with
	// health, restart count and start time from an inspect call per container.
	Containers(ctx context.Context) ([]Container, error)
	// Logs returns the last tail lines of combined stdout/stderr for the
	// named container, with Docker's multiplexed stream framing stripped.
	Logs(ctx context.Context, name string, tail int) (string, error)
	// DiskUsage reports image and build-cache disk usage.
	DiskUsage(ctx context.Context) (DiskUsage, error)
	// PruneImages removes unused images (dangling-only when dangling is
	// true, all unused images otherwise) and returns bytes reclaimed.
	PruneImages(ctx context.Context, dangling bool) (int64, error)
	// Exec runs `<binary> exec <container> <args...>` and returns its
	// combined, trimmed output.
	Exec(ctx context.Context, container string, args ...string) (string, error)
}
