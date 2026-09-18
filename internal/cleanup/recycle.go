package cleanup

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// recyclePlanner plans removal of recycle-bin entries (config.Checks.
// RecycleDirs) older than config.Cleanup.RecycleMinAge. There is no Phase
// 2 check with this exact per-entry rule to extract from — recycle_bin_
// size (internal/checks/disk/dir_size.go) only sums the directory's total
// size for a threshold warning — so this planner lists each directory
// itself.
type recyclePlanner struct{}

func (recyclePlanner) Kind() Kind { return KindRecycle }

func (recyclePlanner) Nodes() []config.Node { return check.NASOnly }

func (recyclePlanner) Plan(ctx context.Context, d check.Deps) (Plan, error) {
	dirs := d.Cfg.Checks.RecycleDirs
	if d.Host == nil || len(dirs) == 0 {
		return Plan{}, check.ErrNotConfigured
	}

	plan := Plan{Kind: KindRecycle, Node: d.Node}
	for _, dir := range dirs {
		entries, err := d.Host.ListDir(ctx, dir)
		if err != nil {
			return Plan{}, fmt.Errorf("cleanup: recycle: listdir %s: %w", dir, err)
		}
		for _, e := range entries {
			if d.Now().Sub(e.ModTime) < d.Cfg.Cleanup.RecycleMinAge {
				continue
			}
			full := filepath.Join(dir, e.Name)
			size, err := recycleEntrySize(ctx, d, full, e.IsDir, e.Size)
			if err != nil {
				return Plan{}, err
			}
			plan.Items = append(plan.Items, PlanItem{
				Key:    full,
				Detail: fmt.Sprintf("recycle bin entry, modified %s", e.ModTime.Format(time.DateOnly)),
				Bytes:  size,
			})
			plan.Bytes += size
		}
	}
	return plan, nil
}

// recycleEntrySize returns the byte size to attribute to one recycle-bin
// entry: a directory's own stat size (from ListDir) reflects only its
// inode, not its contents, so directories are re-measured with DirSize;
// files use the size ListDir already reported.
func recycleEntrySize(ctx context.Context, d check.Deps, path string, isDir bool, statSize int64) (int64, error) {
	if !isDir {
		return statSize, nil
	}
	size, err := d.Host.DirSize(ctx, path, 0)
	if err != nil {
		return 0, fmt.Errorf("cleanup: recycle: dirsize %s: %w", path, err)
	}
	return size, nil
}
