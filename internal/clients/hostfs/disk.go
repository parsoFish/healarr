package hostfs

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Usage reports space usage for the filesystem containing path, via
// syscall.Statfs.
func (o *OS) Usage(ctx context.Context, path string) (Usage, error) {
	if err := ctx.Err(); err != nil {
		return Usage{}, err
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return Usage{}, fmt.Errorf("hostfs: statfs %s: %w", path, err)
	}

	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	free := stat.Bavail * bsize
	used := (stat.Blocks - stat.Bfree) * bsize

	var pct float64
	if denom := used + free; denom > 0 {
		pct = float64(used) / float64(denom) * 100
	}

	return Usage{
		Path:        path,
		Total:       total,
		Free:        free,
		Used:        used,
		UsedPercent: pct,
	}, nil
}

// DirSize sums regular file sizes under root, without following symlinks
// (filepath.WalkDir never descends into a symlinked directory). maxDepth
// caps how many directory levels below root are descended into; root's
// direct children are depth 1. maxDepth <= 0 means unlimited.
func (o *OS) DirSize(ctx context.Context, root string, maxDepth int) (int64, error) {
	rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))

	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("hostfs: walk %s: %w", path, err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		if maxDepth > 0 && path != root {
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - rootDepth
			if depth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return fmt.Errorf("hostfs: stat %s: %w", path, err)
			}
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("hostfs: dirsize %s: %w", root, err)
	}
	return total, nil
}

// ListDir returns path's immediate children, sorted by name.
func (o *OS) ListDir(ctx context.Context, path string) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	dirEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("hostfs: readdir %s: %w", path, err)
	}

	entries := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		info, err := de.Info()
		if err != nil {
			return nil, fmt.Errorf("hostfs: stat %s: %w", filepath.Join(path, de.Name()), err)
		}
		entries = append(entries, Entry{
			Name:    de.Name(),
			Size:    info.Size(),
			IsDir:   de.IsDir(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}
