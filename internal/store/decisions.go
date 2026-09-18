package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrDecisionNotFound is returned by MarkDecision when id doesn't match any
// decisions row.
var ErrDecisionNotFound = errors.New("store: decision not found")

// ErrBadDecisionStatus is returned by MarkDecision when status isn't one of
// executed|failed|blocked. A decision is created pending (CreateDecision)
// and can only be resolved to one of those three terminal states.
var ErrBadDecisionStatus = errors.New("store: bad decision status")

// ErrBadDecisionFailure is returned by MarkDecision when status is
// failed or blocked but cause is nil. A nil cause there is a caller bug
// (fail fast, per project convention, mirroring MarkEmailFailed): the
// decisions.error column exists specifically to record what went wrong, and
// a failed/blocked decision without a cause would silently lose that.
var ErrBadDecisionFailure = errors.New("store: mark decision failed/blocked requires a non-nil cause")

var validDecisionStatuses = map[string]struct{}{"executed": {}, "failed": {}, "blocked": {}}

// Decision mirrors one decisions row: a human/agent choice about an entity
// (e.g. "keep" to snooze, or a remediation trigger) that starts pending and
// is later resolved by MarkDecision.
type Decision struct {
	ID          int64
	EntityKey   string
	Kind        string
	Status      string // pending|executed|failed|blocked
	SnoozeUntil *time.Time
	RequestedAt time.Time
	ExecutedAt  *time.Time
	Error       string
}

// CreateDecision inserts a new pending decision for entityKey and returns
// its id.
func (s *Store) CreateDecision(ctx context.Context, entityKey, kind string, at time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO decisions (entity_key, kind, status, requested_at)
		VALUES (?, ?, 'pending', ?)`,
		entityKey, kind, formatTime(at),
	)
	if err != nil {
		return 0, fmt.Errorf("store: create decision: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: created decision id: %w", err)
	}
	return id, nil
}

// PendingDecisions lists status="pending" rows oldest first, for the
// decision runner to drain.
func (s *Store) PendingDecisions(ctx context.Context) (out []Decision, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, entity_key, kind, status, snooze_until, requested_at, executed_at, error
		FROM decisions
		WHERE status = 'pending'
		ORDER BY requested_at ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: pending decisions: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("store: close pending decisions rows: %w", closeErr))
		}
	}()

	for rows.Next() {
		d, scanErr := scanDecisionRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("store: scan pending decision: %w", scanErr)
		}
		out = append(out, d)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("store: iterate pending decisions: %w", rowsErr)
	}
	return out, nil
}

// DecisionByID returns the decision with id, or ok=false when none exists.
func (s *Store) DecisionByID(ctx context.Context, id int64) (Decision, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, entity_key, kind, status, snooze_until, requested_at, executed_at, error
		FROM decisions
		WHERE id = ?`,
		id,
	)

	d, err := scanDecisionRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Decision{}, false, nil
		}
		return Decision{}, false, fmt.Errorf("store: decision %d: %w", id, err)
	}
	return d, true, nil
}

// MarkDecision resolves a pending decision to a terminal status
// (executed|failed|blocked), stamping executed_at. cause may be nil when
// status is "executed"; it is required for "failed" and "blocked" (see
// ErrBadDecisionFailure), and its message becomes the stored error. It
// returns ErrDecisionNotFound when id doesn't match any row.
func (s *Store) MarkDecision(ctx context.Context, id int64, status string, at time.Time, cause error) error {
	if _, ok := validDecisionStatuses[status]; !ok {
		return fmt.Errorf("store: mark decision %d status %q: %w", id, status, ErrBadDecisionStatus)
	}
	if status != "executed" && cause == nil {
		return fmt.Errorf("store: mark decision %d %s: %w", id, status, ErrBadDecisionFailure)
	}

	errMsg := ""
	if cause != nil {
		errMsg = cause.Error()
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE decisions SET status = ?, executed_at = ?, error = ? WHERE id = ?`,
		status, formatTime(at), errMsg, id,
	)
	if err != nil {
		return fmt.Errorf("store: mark decision %d %s: %w", id, status, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: mark decision %d %s rows affected: %w", id, status, err)
	}
	if n == 0 {
		return fmt.Errorf("store: mark decision %d %s: %w", id, status, ErrDecisionNotFound)
	}
	return nil
}

// SnoozeEntity snoozes every open/snoozed finding whose entity_key matches
// (across all check ids and nodes; entity keys such as "sonarr:12" are
// node-agnostic), setting status='snoozed' and snooze_until=until. It is
// the finding-side effect of a "keep" decision; the decision row itself
// (CreateDecision/MarkDecision) is managed separately by the caller. It is
// not an error for entityKey to currently have no open findings.
func (s *Store) SnoozeEntity(ctx context.Context, entityKey string, until time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE findings
		SET status = 'snoozed', snooze_until = ?
		WHERE entity_key = ? AND status IN ('open', 'snoozed')`,
		formatTime(until), entityKey,
	); err != nil {
		return fmt.Errorf("store: snooze entity %s: %w", entityKey, err)
	}
	return nil
}

// SnoozedUntil returns the latest snooze_until among entityKey's currently
// snoozed findings, ok=false when none of its findings are snoozed.
func (s *Store) SnoozedUntil(ctx context.Context, entityKey string) (time.Time, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT snooze_until FROM findings
		WHERE entity_key = ? AND status = 'snoozed' AND snooze_until IS NOT NULL
		ORDER BY snooze_until DESC
		LIMIT 1`,
		entityKey,
	)

	var untilStr string
	if err := row.Scan(&untilStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("store: snoozed until %s: %w", entityKey, err)
	}

	until, err := time.Parse(time.RFC3339, untilStr)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: parse snoozed until %s: %w", entityKey, err)
	}
	return until, true, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so
// scanDecisionRow works for DecisionByID's single row and
// PendingDecisions' iteration alike.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDecisionRow(row rowScanner) (Decision, error) {
	var (
		id                                              int64
		entityKey, kind, status, requestedAtStr, errMsg string
		snoozeUntilStr, executedAtStr                   sql.NullString
	)
	if err := row.Scan(&id, &entityKey, &kind, &status, &snoozeUntilStr, &requestedAtStr, &executedAtStr, &errMsg); err != nil {
		return Decision{}, err
	}

	requestedAt, err := time.Parse(time.RFC3339, requestedAtStr)
	if err != nil {
		return Decision{}, fmt.Errorf("store: parse decision.requested_at: %w", err)
	}

	var snoozeUntil *time.Time
	if snoozeUntilStr.Valid {
		t, err := time.Parse(time.RFC3339, snoozeUntilStr.String)
		if err != nil {
			return Decision{}, fmt.Errorf("store: parse decision.snooze_until: %w", err)
		}
		snoozeUntil = &t
	}

	var executedAt *time.Time
	if executedAtStr.Valid {
		t, err := time.Parse(time.RFC3339, executedAtStr.String)
		if err != nil {
			return Decision{}, fmt.Errorf("store: parse decision.executed_at: %w", err)
		}
		executedAt = &t
	}

	return Decision{
		ID:          id,
		EntityKey:   entityKey,
		Kind:        kind,
		Status:      status,
		SnoozeUntil: snoozeUntil,
		RequestedAt: requestedAt,
		ExecutedAt:  executedAt,
		Error:       errMsg,
	}, nil
}
