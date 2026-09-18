// Package cleanup plans and executes the disk/torrent/image cleanups that
// mirror Phase 2's read-only checks: recycle-bin entries, orphaned
// downloads, dangling Docker images, and seeded-and-imported torrents.
// Planning is always safe (it only reads); executing is gated behind
// config.Actions.Enabled and config.Cleanup.DryRun (see Execute), and even
// then a host-filesystem removal is refused unless the path sits under a
// directory the operator actually configured (see the recycle/orphans
// executors).
package cleanup

import (
	"context"
	"errors"
	"fmt"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// Kind names one cleanup family.
type Kind string

const (
	KindRecycle Kind = "recycle"
	KindOrphans Kind = "orphans"
	KindDocker  Kind = "docker"
	KindSeeded  Kind = "seeded"
)

// PlanItem is one thing a Plan proposes to remove: a host path (recycle,
// orphans), a torrent hash (seeded), or an aggregate label (docker, which
// has no per-image granularity available from DiskUsage).
type PlanItem struct {
	Key    string
	Detail string
	Bytes  int64
}

// Plan is what a Planner found: zero or more PlanItems and the total bytes
// they'd reclaim if executed.
type Plan struct {
	Kind  Kind
	Node  config.Node
	Items []PlanItem
	Bytes int64
}

// Result is what Execute actually did: how many items it removed, how
// many bytes that reclaimed, and any per-item failures (a partial
// failure doesn't stop the rest of the plan from being attempted).
type Result struct {
	Kind     Kind
	Executed int
	Bytes    int64
	Errors   []string
}

// ErrActionsDisabled is returned by Execute when the operator hasn't
// opted into mutating actions (config.Actions.Enabled is false) or the
// cleanup subsystem's own conservative default is still on
// (config.Cleanup.DryRun is true, the default).
var ErrActionsDisabled = errors.New("cleanup: actions are disabled in config")

// Planner is one cleanup family: it knows which node(s) it applies to and
// how to build a read-only Plan from check.Deps.
type Planner interface {
	Kind() Kind
	Nodes() []config.Node
	Plan(ctx context.Context, d check.Deps) (Plan, error)
}

// All returns every registered planner, in a fixed order (recycle,
// orphans, docker, seeded).
func All() []Planner {
	return []Planner{
		recyclePlanner{},
		orphansPlanner{},
		dockerPlanner{},
		seededPlanner{},
	}
}

// Execute runs p's plan for real. It checks the actions gate first,
// before making any client call: ErrActionsDisabled comes back untouched
// by whatever kind of plan was handed in, so a caller never needs to
// special-case "was anything actually attempted" for that error.
func Execute(ctx context.Context, d check.Deps, p Plan) (Result, error) {
	if !d.Cfg.Actions.Enabled || d.Cfg.Cleanup.DryRun {
		return Result{}, ErrActionsDisabled
	}

	switch p.Kind {
	case KindRecycle, KindOrphans:
		return executeHostRemove(ctx, d, p)
	case KindDocker:
		return executeDocker(ctx, d, p)
	case KindSeeded:
		return executeSeeded(ctx, d, p)
	default:
		return Result{}, fmt.Errorf("cleanup: execute: unknown kind %q", p.Kind)
	}
}
