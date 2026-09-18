package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// FindingHistory returns node's findings (open, snoozed and resolved alike)
// whose last_seen is at or after since, newest last_seen first, capped at
// limit rows. limit is passed straight through to SQL's LIMIT, so SQLite's
// own semantics apply (a negative limit is unbounded); the web UI and CLI
// pass a positive page size.
func (s *Store) FindingHistory(ctx context.Context, node config.Node, since time.Time, limit int) (out []StoredFinding, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, check_id, node, entity_key, severity, tier, summary, detail, data, status,
		       first_seen, last_seen, resolved_at, snooze_until, seen_count
		FROM findings
		WHERE node = ? AND last_seen >= ?
		ORDER BY last_seen DESC, id DESC
		LIMIT ?`,
		string(node), formatTime(since), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: finding history: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("store: close finding history rows: %w", closeErr))
		}
	}()

	for rows.Next() {
		f, scanErr := scanStoredFinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, f)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("store: iterate finding history: %w", rowsErr)
	}
	return out, nil
}
