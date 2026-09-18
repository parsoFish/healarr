package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// Remediation mirrors one remediations row: a recorded attempt (dry-run or
// real) to act on a finding, e.g. a cleanup delete or an *arr nudge.
type Remediation struct {
	ID         int64
	FindingID  *int64 // nil when the remediation isn't tied to one finding
	Node       config.Node
	Action     string
	Tier       string
	Status     string
	Detail     string
	DryRun     bool
	CreatedAt  time.Time
	FinishedAt *time.Time
}

// RecordRemediation inserts r and returns its new row id. r.CreatedAt is
// stored as given; callers set it (rather than RecordRemediation stamping
// "now") so dry-run plans and later real executions can be recorded with
// their own accurate timestamps.
func (s *Store) RecordRemediation(ctx context.Context, r Remediation) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO remediations (finding_id, node, action, tier, dry_run, status, detail, created_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullInt64(r.FindingID), string(r.Node), r.Action, r.Tier, boolToInt(r.DryRun), r.Status, r.Detail,
		formatTime(r.CreatedAt), nullTime(r.FinishedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("store: record remediation: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: recorded remediation id: %w", err)
	}
	return id, nil
}

// RecentRemediations lists remediations created since (inclusive), newest
// first, for the digest and the web UI's activity view.
func (s *Store) RecentRemediations(ctx context.Context, since time.Time) (out []Remediation, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, finding_id, node, action, tier, dry_run, status, detail, created_at, finished_at
		FROM remediations
		WHERE created_at >= ?
		ORDER BY created_at DESC, id DESC`,
		formatTime(since),
	)
	if err != nil {
		return nil, fmt.Errorf("store: recent remediations: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("store: close recent remediations rows: %w", closeErr))
		}
	}()

	for rows.Next() {
		r, scanErr := scanRemediation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, r)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("store: iterate recent remediations: %w", rowsErr)
	}
	return out, nil
}

func scanRemediation(rows *sql.Rows) (Remediation, error) {
	var (
		id                                                  int64
		findingID                                           sql.NullInt64
		nodeStr, action, tier, status, detail, createdAtStr string
		dryRunInt                                           int
		finishedAtStr                                       sql.NullString
	)
	if err := rows.Scan(&id, &findingID, &nodeStr, &action, &tier, &dryRunInt, &status, &detail, &createdAtStr, &finishedAtStr); err != nil {
		return Remediation{}, fmt.Errorf("store: scan remediation: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		return Remediation{}, fmt.Errorf("store: parse remediation.created_at: %w", err)
	}

	var finishedAt *time.Time
	if finishedAtStr.Valid {
		t, err := time.Parse(time.RFC3339, finishedAtStr.String)
		if err != nil {
			return Remediation{}, fmt.Errorf("store: parse remediation.finished_at: %w", err)
		}
		finishedAt = &t
	}

	var fID *int64
	if findingID.Valid {
		id := findingID.Int64
		fID = &id
	}

	return Remediation{
		ID:         id,
		FindingID:  fID,
		Node:       config.Node(nodeStr),
		Action:     action,
		Tier:       tier,
		Status:     status,
		Detail:     detail,
		DryRun:     dryRunInt != 0,
		CreatedAt:  createdAt,
		FinishedAt: finishedAt,
	}, nil
}

// boolToInt renders a Go bool as the 0/1 SQLite stores in an INTEGER
// column, rather than relying on the driver's own bool handling.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullInt64 converts a possibly-nil pointer into the sql.NullInt64 a
// nullable INTEGER column expects.
func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

// nullTime converts a possibly-nil *time.Time into the RFC 3339 UTC
// sql.NullString a nullable TEXT timestamp column expects.
func nullTime(v *time.Time) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*v), Valid: true}
}
