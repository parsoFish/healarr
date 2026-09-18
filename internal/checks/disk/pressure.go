package disk

import (
	"context"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/config"
)

const (
	diskPressurePiSDID      = "disk_pressure_pi_sd"
	diskPressureNASVolumeID = "disk_pressure_nas_volume"
)

// pressure builds a disk_pressure_* check: it watches every path in
// check.Deps.Cfg.Checks.DiskPaths for free-space pressure at Run time,
// using that same Cfg's DiskWarnPercent/DiskCritPercent as thresholds.
// Both disk_pressure ids share this one function; which paths and
// thresholds they see comes from the node's own config file (the Pi's
// lists its SD card, the NAS's its storage volume), so cfg here is unused
// — it exists only so this constructor's signature matches the brief.
func pressure(id string, nodes []config.Node, tier check.Tier, cadence time.Duration, cfg config.Config) check.Check {
	return check.Check{
		ID:      id,
		Nodes:   nodes,
		Tier:    tier,
		Cadence: cadence,
		Run: func(ctx context.Context, d check.Deps) (check.Result, error) {
			return runPressure(ctx, d, id, tier)
		},
	}
}

func runPressure(ctx context.Context, d check.Deps, id string, tier check.Tier) (check.Result, error) {
	paths := d.Cfg.Checks.DiskPaths
	if d.Host == nil || len(paths) == 0 {
		return check.Result{}, check.ErrNotConfigured
	}

	var findings []check.Finding
	metrics := make(map[string]float64, len(paths))
	for _, path := range paths {
		usage, err := d.Host.Usage(ctx, path)
		if err != nil {
			return check.Result{}, fmt.Errorf("usage %s: %w", path, err)
		}
		metrics["disk_used_percent:"+path] = usage.UsedPercent
		if f := pressureFinding(d, id, tier, path, usage); f != nil {
			findings = append(findings, *f)
		}
	}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}

// pressureFinding applies the disk_pressure rule to one path's usage:
// critical at DiskCritPercent, warn at DiskWarnPercent, otherwise healthy.
func pressureFinding(d check.Deps, id string, tier check.Tier, path string, usage hostfs.Usage) *check.Finding {
	summary := fmt.Sprintf("%s is %.0f%% full (%.1f GB free)", path, usage.UsedPercent, float64(usage.Free)/1e9)
	switch {
	case usage.UsedPercent >= d.Cfg.Checks.DiskCritPercent:
		f := d.NewFinding(id, path, check.SeverityCritical, tier, summary)
		return &f
	case usage.UsedPercent >= d.Cfg.Checks.DiskWarnPercent:
		f := d.NewFinding(id, path, check.SeverityWarn, tier, summary)
		return &f
	default:
		return nil
	}
}
