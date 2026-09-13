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
// (filepath.WalkDir never descends into a symlinked directory, and a
// symlink's own DirEntry type is never regular, so a symlink to a file is
// excluded too). maxDepth caps how many directory levels below root are
// descended into; root's direct children are depth 1. maxDepth <= 0 means
// unlimited.
func (o *OS) DirSize(ctx context.Context, root string, maxDepth int) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("hostfs: walk %s: %w", path, err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		if maxDepth > 0 && path != root && depthBelow(root, path) > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
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

// depthBelow returns how many directory levels path sits below root: 0 for
// root itself, 1 for root's direct children, 2 for grandchildren, and so
// on. It is computed from the path relative to root rather than by
// counting separators in the cleaned absolute paths, because that naive
// count undercounts by one when root is "/" — root's own leading
// separator is then indistinguishable from a level boundary.
func depthBelow(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
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
	// os.ReadDir already returns entries sorted by filename, but sorting
	// here too makes the Client.ListDir contract ("sorted by name") hold
	// regardless of that (documented but easy-to-miss) os.ReadDir detail,
	// at negligible cost for directory-sized inputs.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}
