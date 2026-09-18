package mounts

import (
	"context"
	"fmt"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

const hostMountHealthID = "host_mount_health"

// hostMountHealthCheck watches the host side of each configured mount:
// that it is actually a mount point, and that it is readable (an
// unreadable NFS export is the condition this detects).
func hostMountHealthCheck() check.Check {
	return check.Check{
		ID:      hostMountHealthID,
		Nodes:   check.BothNodes,
		Tier:    check.TierObserve,
		Cadence: check.Every5m,
		Run:     runHostMountHealth,
	}
}

func runHostMountHealth(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.Host == nil || len(d.Cfg.Mounts) == 0 {
		return check.Result{}, check.ErrNotConfigured
	}

	var findings []check.Finding
	metrics := map[string]float64{}
	for _, path := range uniqueHostPaths(d.Cfg.Mounts) {
		f, usedPercent, hasMetric, err := inspectHostMount(ctx, d, path)
		if err != nil {
			return check.Result{}, err
		}
		if f != nil {
			findings = append(findings, *f)
		}
		if hasMetric {
			metrics["mount_used_percent:"+path] = usedPercent
		}
	}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}

// uniqueHostPaths returns each mount's host path once, in first-seen
// order — several containers commonly share one host mount.
func uniqueHostPaths(mounts []config.Mount) []string {
	seen := make(map[string]bool, len(mounts))
	paths := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if seen[m.Host] {
			continue
		}
		seen[m.Host] = true
		paths = append(paths, m.Host)
	}
	return paths
}

// inspectHostMount checks one host path: not-a-mountpoint and unreadable
// are both reported as critical findings (their purpose is exactly to
// surface these), never as check errors. A genuine client error from
// IsMountpoint (as opposed to it answering "not mounted") is returned as an
// error.
func inspectHostMount(ctx context.Context, d check.Deps, path string) (finding *check.Finding, usedPercent float64, hasMetric bool, err error) {
	mounted, err := d.Host.IsMountpoint(ctx, path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("is mountpoint %s: %w", path, err)
	}
	if !mounted {
		f := d.NewFinding(hostMountHealthID, path, check.SeverityCritical, check.TierObserve,
			fmt.Sprintf("%s is not a mount point", path))
		return &f, 0, false, nil
	}

	usage, err := d.Host.Usage(ctx, path)
	if err != nil {
		f := d.NewFinding(hostMountHealthID, path, check.SeverityCritical, check.TierObserve,
			fmt.Sprintf("%s unreadable: %s", path, err.Error()))
		return &f, 0, false, nil
	}

	return nil, usage.UsedPercent, true, nil
}
