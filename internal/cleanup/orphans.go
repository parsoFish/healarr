package cleanup

import (
	"context"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/checks/disk"
	"github.com/parsoFish/healarr/internal/config"
)

// orphansPlanner plans removal of downloads directory entries qBittorrent
// no longer accounts for, reusing disk.FindOrphans (the same rule the
// orphan_downloads check uses) and applying config.Cleanup.OrphanMinAge —
// deliberately its own, more conservative threshold than the check's own
// config.Checks.OrphanAfter, since removing something is a bigger step
// than merely flagging it.
type orphansPlanner struct{}

func (orphansPlanner) Kind() Kind { return KindOrphans }

func (orphansPlanner) Nodes() []config.Node { return check.NASOnly }

func (orphansPlanner) Plan(ctx context.Context, d check.Deps) (Plan, error) {
	orphans, err := disk.FindOrphans(ctx, d)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{Kind: KindOrphans, Node: d.Node}
	for _, o := range orphans {
		if d.Now().Sub(o.ModTime) < d.Cfg.Cleanup.OrphanMinAge {
			continue
		}
		plan.Items = append(plan.Items, PlanItem{
			Key:    o.Path,
			Detail: fmt.Sprintf("orphaned download, modified %s", o.ModTime.Format(time.DateOnly)),
			Bytes:  o.Size,
		})
		plan.Bytes += o.Size
	}
	return plan, nil
}
