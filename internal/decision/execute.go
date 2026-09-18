package decision

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/store"
)

// Execute resolves the pending decision with id. "keep" snoozes its
// entity for cfg.Staleness.SnoozeDays and marks the decision executed —
// always allowed, even with actions disabled. "delete" removes the
// entity from Sonarr/Radarr, best-effort declines matching Overseerr
// requests and tells the peer which torrents to remove, gated behind
// cfg.Actions.Enabled. It returns the decision as MarkDecision left it.
func Execute(ctx context.Context, d Deps, id int64) (store.Decision, error) {
	dec, ok, err := d.Store.DecisionByID(ctx, id)
	if err != nil {
		return store.Decision{}, fmt.Errorf("decision: execute %d: %w", id, err)
	}
	if !ok {
		return store.Decision{}, fmt.Errorf("decision: execute %d: %w", id, store.ErrDecisionNotFound)
	}

	switch Kind(dec.Kind) {
	case KindKeep:
		return executeKeep(ctx, d, dec)
	case KindDelete:
		return executeDelete(ctx, d, dec)
	default:
		return store.Decision{}, fmt.Errorf("decision: execute %d: %w: %q", id, ErrUnknownKind, dec.Kind)
	}
}

// executeKeep snoozes dec.EntityKey for cfg.Staleness.SnoozeDays and
// marks dec executed. It never checks cfg.Actions.Enabled: keeping
// something only touches findings' snooze state, never the media stack.
func executeKeep(ctx context.Context, d Deps, dec store.Decision) (store.Decision, error) {
	now := d.Now()
	until := now.AddDate(0, 0, d.Cfg.Staleness.SnoozeDays)

	if err := d.Store.SnoozeEntity(ctx, dec.EntityKey, until); err != nil {
		wrapped := fmt.Errorf("decision: keep %d: snooze %s: %w", dec.ID, dec.EntityKey, err)
		if markErr := d.Store.MarkDecision(ctx, dec.ID, "failed", now, wrapped); markErr != nil {
			return store.Decision{}, errors.Join(wrapped, fmt.Errorf("decision: keep %d: mark failed: %w", dec.ID, markErr))
		}
		return store.Decision{}, wrapped
	}

	if err := d.Store.MarkDecision(ctx, dec.ID, "executed", now, nil); err != nil {
		return store.Decision{}, fmt.Errorf("decision: keep %d: mark executed: %w", dec.ID, err)
	}

	dec.Status = "executed"
	dec.SnoozeUntil = &until
	dec.ExecutedAt = &now
	return dec, nil
}

// blockDelete marks dec blocked with ErrActionsDisabled, records a
// blocked remediation, and returns ErrActionsDisabled — the actions gate
// is checked before any client write (constraints.md), so this never
// touches Sonarr/Radarr/Overseerr/the peer.
func blockDelete(ctx context.Context, d Deps, dec store.Decision, now time.Time) (store.Decision, error) {
	recordDeleteRemediation(ctx, d, dec.ID, "blocked", now, ErrActionsDisabled.Error())
	if err := d.Store.MarkDecision(ctx, dec.ID, "blocked", now, ErrActionsDisabled); err != nil {
		return store.Decision{}, fmt.Errorf("decision: delete %d: mark blocked: %w", dec.ID, errors.Join(ErrActionsDisabled, err))
	}
	return store.Decision{}, fmt.Errorf("decision: delete %d: %w", dec.ID, ErrActionsDisabled)
}

// failDelete marks dec failed with cause, records a failed remediation,
// and returns cause wrapped with dec's id. This is for hard failures
// only (a bad entity key, an unconfigured client, or the *arr delete
// call itself failing) — best-effort steps (Overseerr, the peer hint)
// never reach here.
func failDelete(ctx context.Context, d Deps, dec store.Decision, now time.Time, cause error) (store.Decision, error) {
	recordDeleteRemediation(ctx, d, dec.ID, "failed", now, cause.Error())
	if err := d.Store.MarkDecision(ctx, dec.ID, "failed", now, cause); err != nil {
		return store.Decision{}, fmt.Errorf("decision: delete %d: mark failed: %w", dec.ID, errors.Join(cause, err))
	}
	return store.Decision{}, fmt.Errorf("decision: delete %d: %w", dec.ID, cause)
}

// recordDeleteRemediation records one remediations row for a delete
// decision attempt (status blocked|executed|failed, per constraints.md).
// A failure to record is never fatal to Execute — the decision's own
// outcome is already decided — but it is always logged, so it is never
// silently lost.
func recordDeleteRemediation(ctx context.Context, d Deps, decisionID int64, status string, at time.Time, detail string) {
	r := store.Remediation{
		Node:       d.Node,
		Action:     "decision_delete",
		Tier:       string(check.TierCorrect),
		Status:     status,
		Detail:     detail,
		DryRun:     false,
		CreatedAt:  at,
		FinishedAt: &at,
	}
	if _, err := d.Store.RecordRemediation(ctx, r); err != nil {
		slog.Default().Error("decision: record remediation", "decision_id", decisionID, "status", status, "error", err)
	}
}

// entityRef is a parsed "sonarr:<id>" or "radarr:<id>" entity key.
type entityRef struct {
	Provider string
	ID       int64
}

// parseEntityKey parses key as "sonarr:<id>" or "radarr:<id>". Any other
// shape or provider is ErrUnknownEntityKey.
func parseEntityKey(key string) (entityRef, error) {
	provider, idStr, ok := strings.Cut(key, ":")
	if !ok || (provider != "sonarr" && provider != "radarr") {
		return entityRef{}, fmt.Errorf("%w: %q", ErrUnknownEntityKey, key)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return entityRef{}, fmt.Errorf("%w: %q: %w", ErrUnknownEntityKey, key, err)
	}
	return entityRef{Provider: provider, ID: id}, nil
}
