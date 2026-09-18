package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
)

// Fixed cron specs for the check-cycle cadences RunCycle understands. The
// daily cycle runs at 00:10 so it always precedes the digest (Email.DigestAt,
// default 07:00) by hours and never coincides with the nightly checkpoint
// (Agent.CheckpointAt, default 03:00).
const (
	every5mSpec    = "*/5 * * * *"
	every15mSpec   = "*/15 * * * *"
	hourlySpec     = "0 * * * *"
	dailyCycleSpec = "10 0 * * *"
)

// initialCycleCadence is the cadence Run executes once immediately on
// startup (before the *5m cron entry above would first fire).
const initialCycleCadence = 5 * time.Minute

// cycleCadence pairs one fixed cron spec with the RunCycle cadence it
// triggers.
type cycleCadence struct {
	spec    string
	cadence time.Duration
}

// cycleCadences lists every fixed check-cycle entry Schedule registers,
// regardless of node: RunCycle itself is a no-op for a cadence with no
// matching checks in the registry (see cycle.go), so registering all four
// here unconditionally is safe for both pi and nas.
func cycleCadences() []cycleCadence {
	return []cycleCadence{
		{every5mSpec, initialCycleCadence},
		{every15mSpec, 15 * time.Minute},
		{hourlySpec, time.Hour},
		{dailyCycleSpec, 24 * time.Hour},
	}
}

// SendHeartbeat posts a Heartbeat to the peer and records it as an
// outbound "heartbeat" peer message. A nil peer is a no-op (nothing
// configured to heartbeat to). The push to the peer itself is never
// fatal — an unreachable peer is logged and otherwise ignored, mirroring
// pushReport (cycle.go) — but a local failure recording the outbound
// message is returned, since that indicates a real store problem.
func (a *Agent) SendHeartbeat(ctx context.Context) error {
	if a.peer == nil {
		return nil
	}

	now := a.now()
	hb := peer.Heartbeat{Node: a.cfg.Node, At: now, Version: a.version}

	if _, err := a.peer.Heartbeat(ctx, hb); err != nil {
		a.logger.Error("agent: send heartbeat to peer failed", "node", a.cfg.Node, "error", err)
	}

	payload, err := json.Marshal(hb)
	if err != nil {
		a.logger.Error("agent: marshal outbound heartbeat", "node", a.cfg.Node, "error", err)
		payload = fallbackPayload(err)
	}
	if _, err := a.store.SavePeerMessage(ctx, "out", "heartbeat", otherNode(a.cfg.Node), payload, now); err != nil {
		return fmt.Errorf("agent: send heartbeat: record: %w", err)
	}
	return nil
}

// Schedule builds (but does not start — see Run) the cron scheduler for
// this node: the four fixed check-cycle cadences, a heartbeat every
// Agent.HeartbeatInterval when a peer is configured, a daily digest at
// Email.DigestAt when this agent can send mail (the Pi only), and a daily
// checkpoint at Agent.CheckpointAt. Every job is wrapped with
// cron.Recover (a panicking job is logged, never crashes the daemon) and
// cron.SkipIfStillRunning (an overlapping run of the same job is skipped
// and logged rather than left to pile up), per constraints.md.
func (a *Agent) Schedule() (*cron.Cron, error) {
	loc, err := a.cfg.Location()
	if err != nil {
		return nil, fmt.Errorf("agent: schedule: %w", err)
	}

	chainLogger := newCronLogger(a.logger)
	c := cron.New(
		cron.WithLocation(loc),
		cron.WithChain(cron.Recover(chainLogger), cron.SkipIfStillRunning(chainLogger)),
	)

	if err := a.scheduleCycles(c); err != nil {
		return nil, err
	}
	if err := a.scheduleHeartbeat(c); err != nil {
		return nil, err
	}
	if err := a.scheduleDigest(c); err != nil {
		return nil, err
	}
	if err := a.scheduleCheckpoint(c); err != nil {
		return nil, err
	}
	return c, nil
}

// scheduleCycles registers the four fixed-cadence check cycles.
func (a *Agent) scheduleCycles(c *cron.Cron) error {
	for _, cc := range cycleCadences() {
		cadence := cc.cadence
		if _, err := c.AddFunc(cc.spec, func() { a.runScheduledCycle(cadence) }); err != nil {
			return fmt.Errorf("agent: schedule: cycle %s: %w", cc.spec, err)
		}
	}
	return nil
}

// runScheduledCycle runs one cycle with a background context; a failure is
// logged and never propagated, since a cron job has nowhere to return an
// error to.
func (a *Agent) runScheduledCycle(cadence time.Duration) {
	if _, _, err := a.RunCycle(context.Background(), cadence); err != nil {
		a.logger.Error("agent: scheduled cycle failed", "cadence", cadence, "error", err)
	}
}

// scheduleHeartbeat registers the heartbeat job, only when a peer client is
// configured (a nil peer has nowhere to send one).
func (a *Agent) scheduleHeartbeat(c *cron.Cron) error {
	if a.peer == nil {
		return nil
	}
	spec := fmt.Sprintf("@every %s", a.cfg.Agent.HeartbeatInterval)
	_, err := c.AddFunc(spec, func() {
		if err := a.SendHeartbeat(context.Background()); err != nil {
			a.logger.Error("agent: scheduled heartbeat failed", "error", err)
		}
	})
	if err != nil {
		return fmt.Errorf("agent: schedule: heartbeat: %w", err)
	}
	return nil
}

// scheduleDigest registers the daily digest job, only when this agent can
// send mail (HasSender — the Pi only; constraints.md).
func (a *Agent) scheduleDigest(c *cron.Cron) error {
	if !a.HasSender() {
		return nil
	}
	spec, err := dailyCronSpec(a.cfg.Email.DigestAt)
	if err != nil {
		return fmt.Errorf("agent: schedule: digest: %w", err)
	}
	_, err = c.AddFunc(spec, func() {
		if _, err := a.SendDigest(context.Background()); err != nil {
			a.logger.Error("agent: scheduled digest failed", "error", err)
		}
	})
	if err != nil {
		return fmt.Errorf("agent: schedule: digest: %w", err)
	}
	return nil
}

// scheduleCheckpoint registers the daily WAL-checkpoint job. ErrCheckpointBusy
// is expected (another connection is pinning an older WAL snapshot) and is
// logged at Warn rather than Error: the checkpoint job simply retries the
// next night.
func (a *Agent) scheduleCheckpoint(c *cron.Cron) error {
	spec, err := dailyCronSpec(a.cfg.Agent.CheckpointAt)
	if err != nil {
		return fmt.Errorf("agent: schedule: checkpoint: %w", err)
	}
	_, err = c.AddFunc(spec, func() {
		if err := a.store.Checkpoint(context.Background()); err != nil {
			if errors.Is(err, store.ErrCheckpointBusy) {
				a.logger.Warn("agent: checkpoint busy, retrying next night", "error", err)
				return
			}
			a.logger.Error("agent: scheduled checkpoint failed", "error", err)
		}
	})
	if err != nil {
		return fmt.Errorf("agent: schedule: checkpoint: %w", err)
	}
	return nil
}

// dailyCronSpec turns an "HH:MM" local time-of-day (Agent.CheckpointAt,
// Email.DigestAt) into the 5-field cron spec "<minute> <hour> * * *".
func dailyCronSpec(hhmm string) (string, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return "", fmt.Errorf("invalid \"HH:MM\" time %q: %w", hhmm, err)
	}
	return fmt.Sprintf("%d %d * * *", t.Minute(), t.Hour()), nil
}

// cronLogger adapts a *slog.Logger to cron.Logger, so cron.Recover and
// cron.SkipIfStillRunning route through the daemon's own structured
// logging instead of cron's stdout-writing DefaultLogger.
type cronLogger struct{ logger *slog.Logger }

func newCronLogger(logger *slog.Logger) cronLogger { return cronLogger{logger: logger} }

// Info implements cron.Logger.
func (l cronLogger) Info(msg string, keysAndValues ...any) {
	l.logger.Info("agent: cron: "+msg, keysAndValues...)
}

// Error implements cron.Logger.
func (l cronLogger) Error(err error, msg string, keysAndValues ...any) {
	args := append([]any{"error", err}, keysAndValues...)
	l.logger.Error("agent: cron: "+msg, args...)
}
