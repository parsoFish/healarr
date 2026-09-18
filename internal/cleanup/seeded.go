package cleanup

import (
	"context"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/checks/qbit"
	"github.com/parsoFish/healarr/internal/config"
)

// seededPlanner plans removal of torrents that have seeded to
// qBittorrent's own target and been imported, reusing qbit.SeededDone
// (the same rule the seeded_done check uses).
type seededPlanner struct{}

func (seededPlanner) Kind() Kind { return KindSeeded }

func (seededPlanner) Nodes() []config.Node { return check.NASOnly }

func (seededPlanner) Plan(ctx context.Context, d check.Deps) (Plan, error) {
	torrents, err := qbit.SeededDone(ctx, d)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{Kind: KindSeeded, Node: d.Node}
	for _, t := range torrents {
		hours := int(t.SeedingTime.Round(time.Hour).Hours())
		plan.Items = append(plan.Items, PlanItem{
			Key:    t.Hash,
			Detail: fmt.Sprintf("%s seeded to target (ratio %.2f, %dh)", t.Name, t.Ratio, hours),
			Bytes:  t.Size,
		})
		plan.Bytes += t.Size
	}
	return plan, nil
}

// executeSeeded removes p's torrents via QBit.Delete, never deleting
// their downloaded files (deleteFiles=false): a seeded-and-imported
// torrent's files already live wherever the *arr apps imported them to,
// so only the torrent-client bookkeeping needs to go.
func executeSeeded(ctx context.Context, d check.Deps, p Plan) (Result, error) {
	if d.QBit == nil {
		return Result{}, fmt.Errorf("cleanup: seeded: qbittorrent client not configured")
	}
	if len(p.Items) == 0 {
		return Result{Kind: KindSeeded}, nil
	}

	hashes := make([]string, len(p.Items))
	for i, item := range p.Items {
		hashes[i] = item.Key
	}
	if err := d.QBit.Delete(ctx, hashes, false); err != nil {
		return Result{}, fmt.Errorf("cleanup: seeded: delete torrents: %w", err)
	}
	return Result{Kind: KindSeeded, Executed: len(hashes), Bytes: p.Bytes}, nil
}
