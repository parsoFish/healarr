package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/cleanup"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// runDailyCycleAndCleanupPlanning runs the 00:10 daily check cycle, then
// plans every cleanup.Planner registered for this node — whether or not
// the check cycle itself succeeded, since a failed check cycle shouldn't
// also skip the night's cleanup planning. It never calls cleanup.Execute:
// overnight is observe-only (constraints.md) — planning only reads, so
// it's always safe to run unattended.
func (a *Agent) runDailyCycleAndCleanupPlanning(ctx context.Context, cadence time.Duration) {
	a.runScheduledCycle(ctx, cadence)
	a.planDailyCleanups(ctx)
}

// planDailyCleanups runs every cleanup.Planner whose Nodes() include this
// node, using a fresh check.Deps built from Options.Deps (never the one
// the check cycle just used — a planner reads live client state, not a
// completed report), and records one remediations row per plan. A Deps
// build failure records one failed row per applicable planner (so every
// planner this run would have covered still gets a durable record —
// constraints.md: "background failures logged AND recorded") rather than
// silently skipping the night.
func (a *Agent) planDailyCleanups(ctx context.Context) {
	planners := plannersForNode(a.cfg.Node)
	if len(planners) == 0 {
		return
	}

	deps, err := a.deps(ctx)
	if err != nil {
		a.logger.Error("agent: daily cleanup planning: build deps", "error", err)
		for _, p := range planners {
			a.recordCleanupPlanFailure(ctx, string(p.Kind()), fmt.Sprintf("build deps: %v", err))
		}
		return
	}

	for _, p := range planners {
		a.planOneDailyCleanup(ctx, deps, p)
	}
}

// planOneDailyCleanup plans one cleanup.Planner and records the outcome:
// a planning failure is logged and recorded as a failed dry-run row; a
// successful plan (even an empty one — zero items still proves the
// planner ran) is recorded as a planned dry-run row and logged at Info.
func (a *Agent) planOneDailyCleanup(ctx context.Context, deps check.Deps, planner cleanup.Planner) {
	kind := planner.Kind()
	plan, err := planner.Plan(ctx, deps)
	if err != nil {
		a.logger.Error("agent: daily cleanup planning failed", "kind", kind, "error", err)
		a.recordCleanupPlanFailure(ctx, string(kind), err.Error())
		return
	}

	now := a.now()
	r := store.Remediation{
		Node:      a.cfg.Node,
		Action:    "cleanup:" + string(kind),
		Tier:      string(check.TierCorrect),
		Status:    "planned",
		Detail:    fmt.Sprintf("%d items, %d bytes", len(plan.Items), plan.Bytes),
		DryRun:    true,
		CreatedAt: now,
	}
	if _, err := a.store.RecordRemediation(ctx, r); err != nil {
		a.logger.Error("agent: record cleanup plan", "kind", kind, "error", err)
		return
	}
	a.logger.Info("agent: cleanup plan recorded", "kind", kind, "items", len(plan.Items), "bytes", plan.Bytes)
}

// recordCleanupPlanFailure records one failed dry-run remediations row
// for a cleanup planning attempt. A failure to record is only logged —
// never fatal — since the caller already logged the underlying error.
func (a *Agent) recordCleanupPlanFailure(ctx context.Context, kind, detail string) {
	r := store.Remediation{
		Node:      a.cfg.Node,
		Action:    "cleanup:" + kind,
		Tier:      string(check.TierCorrect),
		Status:    "failed",
		Detail:    detail,
		DryRun:    true,
		CreatedAt: a.now(),
	}
	if _, err := a.store.RecordRemediation(ctx, r); err != nil {
		a.logger.Error("agent: record cleanup plan failure", "kind", kind, "error", err)
	}
}

// plannersForNode returns every cleanup.All() planner whose Nodes()
// include node, preserving cleanup.All()'s fixed order.
func plannersForNode(node config.Node) []cleanup.Planner {
	var out []cleanup.Planner
	for _, p := range cleanup.All() {
		for _, n := range p.Nodes() {
			if n == node {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
