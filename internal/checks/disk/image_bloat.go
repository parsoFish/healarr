package disk

import (
	"context"
	"fmt"

	"github.com/parsoFish/healarr/internal/check"
)

const imageBloatID = "docker_image_bloat"

// imageBloatCheck watches Docker's reclaimable image space (GET
// /system/df's ImagesReclaimable): images superseded by a newer pull that
// nothing removes automatically, since this check never calls PruneImages
// itself — it only observes and reports (see the family's pure-checks
// rule).
func imageBloatCheck() check.Check {
	return check.Check{
		ID:      imageBloatID,
		Nodes:   check.PiOnly,
		Tier:    check.TierNudge,
		Cadence: check.Daily,
		Run:     runImageBloat,
	}
}

func runImageBloat(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.Docker == nil {
		return check.Result{}, check.ErrNotConfigured
	}
	usage, err := d.Docker.DiskUsage(ctx)
	if err != nil {
		return check.Result{}, fmt.Errorf("docker disk usage: %w", err)
	}

	metrics := map[string]float64{"docker_images_reclaimable_bytes": float64(usage.ImagesReclaimable)}
	warnBytes := d.Cfg.Checks.ImageBloatWarnGB * 1e9
	if float64(usage.ImagesReclaimable) < warnBytes {
		return check.Result{Metrics: metrics}, nil
	}

	f := d.NewFinding(imageBloatID, "docker:images", check.SeverityWarn, check.TierNudge,
		fmt.Sprintf("%.1f GB of docker images reclaimable", float64(usage.ImagesReclaimable)/1e9))
	return check.Result{Findings: []check.Finding{f}, Metrics: metrics}, nil
}
