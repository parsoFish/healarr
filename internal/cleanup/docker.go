package cleanup

import (
	"context"
	"fmt"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// dockerImagesKey is the single aggregate PlanItem key the docker planner
// produces: GET /system/df (DiskUsage) reports total reclaimable image
// bytes, not a per-image breakdown, so there is no finer-grained key to
// offer here (unlike recycle/orphans' host paths or seeded's torrent
// hashes).
const dockerImagesKey = "docker:images"

// dockerPlanner plans a Docker image prune, reusing DiskUsage exactly as
// the docker_image_bloat check does (internal/checks/disk/image_bloat.go)
// to estimate reclaimable bytes.
type dockerPlanner struct{}

func (dockerPlanner) Kind() Kind { return KindDocker }

func (dockerPlanner) Nodes() []config.Node { return check.PiOnly }

func (dockerPlanner) Plan(ctx context.Context, d check.Deps) (Plan, error) {
	if d.Docker == nil {
		return Plan{}, check.ErrNotConfigured
	}
	usage, err := d.Docker.DiskUsage(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("cleanup: docker: disk usage: %w", err)
	}

	plan := Plan{Kind: KindDocker, Node: d.Node}
	if usage.ImagesReclaimable <= 0 {
		return plan, nil
	}

	scope := "unused"
	if d.Cfg.Cleanup.DockerDangling {
		scope = "dangling"
	}
	plan.Items = append(plan.Items, PlanItem{
		Key:    dockerImagesKey,
		Detail: fmt.Sprintf("%.1f GB of %s images reclaimable", float64(usage.ImagesReclaimable)/1e9, scope),
		Bytes:  usage.ImagesReclaimable,
	})
	plan.Bytes = usage.ImagesReclaimable
	return plan, nil
}

// executeDocker prunes images via PruneImages(config.Cleanup.
// DockerDangling). The actual bytes reclaimed (Result.Bytes) comes from
// PruneImages's own return value rather than the plan's estimate: when
// DockerDangling is true, the prune only removes dangling images, which
// can reclaim less than DiskUsage's broader "every unused image"
// estimate.
func executeDocker(ctx context.Context, d check.Deps, p Plan) (Result, error) {
	if d.Docker == nil {
		return Result{}, fmt.Errorf("cleanup: docker: docker client not configured")
	}
	reclaimed, err := d.Docker.PruneImages(ctx, d.Cfg.Cleanup.DockerDangling)
	if err != nil {
		return Result{}, fmt.Errorf("cleanup: docker: prune images: %w", err)
	}
	return Result{Kind: KindDocker, Executed: len(p.Items), Bytes: reclaimed}, nil
}
