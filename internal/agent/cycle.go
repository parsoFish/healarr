package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/peer"
)

// RunCycle runs every check for this node whose Cadence == cadence, saves
// the resulting report (Deps.Previous is set from the latest stored
// report before the checks run), and — when a.peer != nil and this node
// is the NAS — pushes the saved report to the peer. A push failure is
// logged and recorded as a failed outbound message but never returned:
// only a Deps or Store failure produces a non-nil error here.
//
// An unknown cadence (no checks in the registry for this node run at that
// cadence) short-circuits before touching Deps or the store: it returns
// an empty report, id 0, and a nil error.
func (a *Agent) RunCycle(ctx context.Context, cadence time.Duration) (check.Report, int64, error) {
	checks := checksForCadence(a.registry.ForNode(a.cfg.Node), cadence)
	if len(checks) == 0 {
		return emptyReport(a.cfg.Node, a.now()), 0, nil
	}

	deps, err := a.deps(ctx)
	if err != nil {
		return check.Report{}, 0, fmt.Errorf("agent: run cycle: build deps: %w", err)
	}

	prev, found, err := a.store.LatestReport(ctx, a.cfg.Node)
	if err != nil {
		return check.Report{}, 0, fmt.Errorf("agent: run cycle: latest report: %w", err)
	}
	if found {
		deps.Previous = &prev
	}

	rep := check.Run(ctx, checks, deps, a.cfg.Checks.Timeout)

	id, _, err := a.store.SaveReport(ctx, rep)
	if err != nil {
		return check.Report{}, 0, fmt.Errorf("agent: run cycle: save report: %w", err)
	}

	pushed := false
	if a.peer != nil && a.cfg.Node == config.NodeNAS {
		pushed = a.pushReport(ctx, rep)
	}

	a.logger.Info("agent: cycle complete",
		"cadence", cadence,
		"checks_run", rep.ChecksRun,
		"checks_failed", rep.ChecksFailed,
		"findings", len(rep.Findings),
		"report_id", id,
		"pushed", pushed,
	)
	return rep, id, nil
}

// checksForCadence filters checks to those whose Cadence equals cadence,
// preserving registry order.
func checksForCadence(checks []check.Check, cadence time.Duration) []check.Check {
	out := make([]check.Check, 0, len(checks))
	for _, c := range checks {
		if c.Cadence == cadence {
			out = append(out, c)
		}
	}
	return out
}

// emptyReport mirrors check.Run's zero-value construction (non-nil
// slices/maps) so a cycle with no matching checks still returns a report
// callers can render or JSON-encode without a nil-pointer surprise.
func emptyReport(node config.Node, at time.Time) check.Report {
	return check.Report{
		Node:        node,
		GeneratedAt: at,
		Findings:    []check.Finding{},
		Ran:         []string{},
		Skipped:     []string{},
		Errors:      []check.CheckError{},
		Metrics:     map[string]float64{},
	}
}

// pushReport sends rep to the peer as a ReportEnvelope and records the
// attempt as an outbound peer message, regardless of whether the push
// succeeded; it reports whether the peer accepted it, for the cycle's own
// summary line. A push or marshal failure is logged (never fatal to the
// cycle) but the message is still recorded, so the peer_messages log
// reflects every attempt made, not just the successful ones.
func (a *Agent) pushReport(ctx context.Context, rep check.Report) bool {
	now := a.now()
	env := peer.ReportEnvelope{Node: a.cfg.Node, SentAt: now, Version: a.version, Report: rep}

	pushed := true
	if _, err := a.peer.PushReport(ctx, env); err != nil {
		a.logger.Error("agent: push report to peer failed", "node", a.cfg.Node, "error", err)
		pushed = false
	}

	payload, err := json.Marshal(env)
	if err != nil {
		a.logger.Error("agent: marshal outbound report envelope", "node", a.cfg.Node, "error", err)
		payload = fallbackPayload(err)
	}
	if _, err := a.store.SavePeerMessage(ctx, "out", "report", otherNode(a.cfg.Node), payload, now); err != nil {
		a.logger.Error("agent: record outbound peer message failed", "node", a.cfg.Node, "error", err)
	}
	return pushed
}
