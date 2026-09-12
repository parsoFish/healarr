// Package hostfs probes the host filesystem: the mount table, device IDs,
// disk usage, and directory contents and sizes. It has no HTTP surface; it
// reads /proc/self/mountinfo and calls into Linux syscalls directly, so it
// is Linux-only (both healarr build targets are linux/amd64 and
// linux/arm64).
package hostfs

import (
	"context"
	"time"
)

// Mount is one entry of the kernel mount table (one line of
// /proc/self/mountinfo).
type Mount struct {
	// Target is the mount point (field 5 of mountinfo), with octal escapes
	// such as \040 (space) decoded.
	Target string
	// Source is the mount source (e.g. a device path or "host:/export"),
	// decoded the same way as Target.
	Source string
	// FSType is the filesystem type, e.g. "ext4", "nfs", "autofs".
	FSType string
	// Options are the per-mount options (field 6 of mountinfo), split on
	// comma.
	Options []string
}

// Usage summarises free/used space for the filesystem containing Path, in
// bytes.
type Usage struct {
	Path string
	// Total is the filesystem's total size.
	Total uint64
	// Free is space available to unprivileged users (statfs Bavail).
	Free uint64
	// Used is Total minus the filesystem's free-block count (statfs
	// Bfree), which can exceed Total-Free when blocks are reserved for
	// the superuser.
	Used uint64
	// UsedPercent is Used as a percentage of Used+Free, in [0, 100].
	UsedPercent float64
}

// Entry is one file or directory returned by ListDir.
type Entry struct {
	Name    string
	Size    int64
	IsDir   bool
	ModTime time.Time
}

// Client is the behaviour the rest of healarr depends on for host
// filesystem probes.
type Client interface {
	// Mounts parses the mount table (normally /proc/self/mountinfo).
	Mounts(ctx context.Context) ([]Mount, error)
	// IsMountpoint reports whether path is itself a mount point: its
	// device differs from its parent directory's device, or it is "/".
	IsMountpoint(ctx context.Context, path string) (bool, error)
	// DeviceID returns path's underlying device number (stat st_dev),
	// comparable with a container-side `stat -c %d` on the same path.
	DeviceID(ctx context.Context, path string) (uint64, error)
	// Usage reports space usage for the filesystem containing path.
	Usage(ctx context.Context, path string) (Usage, error)
	// DirSize sums regular file sizes under path without following
	// symlinks. maxDepth limits how many directory levels below path are
	// descended into (0 means unlimited); path's direct children are
	// depth 1.
	DirSize(ctx context.Context, path string, maxDepth int) (int64, error)
	// ListDir returns path's immediate children, sorted by name.
	ListDir(ctx context.Context, path string) ([]Entry, error)
}
