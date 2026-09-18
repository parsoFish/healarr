package disk

import (
	"context"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

const (
	logSizeID        = "log_size"
	recycleBinSizeID = "recycle_bin_size"
)

// logDirs/logWarnGB and recycleDirs/recycleWarnGB adapt config.Checks's log
// and recycle-bin fields to dirSize's factory-function parameters, so
// dirSize itself stays generic over which pair of (dir list, threshold)
// fields it reads.
func logDirs(cfg config.Config) []string      { return cfg.Checks.LogDirs }
func logWarnGB(cfg config.Config) float64     { return cfg.Checks.LogWarnGB }
func recycleDirs(cfg config.Config) []string  { return cfg.Checks.RecycleDirs }
func recycleWarnGB(cfg config.Config) float64 { return cfg.Checks.RecycleWarnGB }

// dirSize builds a directory-size check shared by log_size and
// recycle_bin_size: for each directory dirs(d.Cfg) names, it sums file
// sizes with DirSize and warns once that total clears warnGB(d.Cfg)
// gigabytes. dirs and warnGB are applied to check.Deps.Cfg at Run time —
// not to any cfg captured at construction — so the same catalogue row
// keeps reading whichever config the runner hands it.
func dirSize(id string, nodes []config.Node, tier check.Tier, cadence time.Duration, dirs func(config.Config) []string, warnGB func(config.Config) float64, label string) check.Check {
	return check.Check{
		ID:      id,
		Nodes:   nodes,
		Tier:    tier,
		Cadence: cadence,
		Run: func(ctx context.Context, d check.Deps) (check.Result, error) {
			return runDirSize(ctx, d, id, tier, dirs, warnGB, label)
		},
	}
}

func runDirSize(ctx context.Context, d check.Deps, id string, tier check.Tier, dirs func(config.Config) []string, warnGB func(config.Config) float64, label string) (check.Result, error) {
	list := dirs(d.Cfg)
	if d.Host == nil || len(list) == 0 {
		return check.Result{}, check.ErrNotConfigured
	}

	warnBytes := warnGB(d.Cfg) * 1e9
	var findings []check.Finding
	metrics := make(map[string]float64, len(list))
	for _, dir := range list {
		size, err := d.Host.DirSize(ctx, dir, 0)
		if err != nil {
			return check.Result{}, fmt.Errorf("dirsize %s: %w", dir, err)
		}
		metrics[id+"_bytes:"+dir] = float64(size)
		if float64(size) >= warnBytes {
			f := d.NewFinding(id, dir, check.SeverityWarn, tier,
				fmt.Sprintf("%s %s is %.1f GB", label, dir, float64(size)/1e9))
			findings = append(findings, f)
		}
	}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}
