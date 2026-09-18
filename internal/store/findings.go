package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// StoredFinding is a persisted Finding plus the store's own bookkeeping.
type StoredFinding struct {
	ID int64
	check.Finding
	Status     string // open|snoozed|resolved
	SeenCount  int
	ResolvedAt *time.Time
}

// upsertFindings applies the dedup rule inside rep's SaveReport transaction:
//  1. each finding in rep.Findings either updates a matching open/snoozed
//     row (same node+check_id+entity_key) or inserts a new one;
//  2. every check id in rep.Ran (successful checks only) resolves any open
//     finding for (node, check_id) whose entity_key wasn't seen this run.
func upsertFindings(ctx context.Context, tx *sql.Tx, rep check.Report) (UpsertSummary, error) {
	var summary UpsertSummary

	seenKeys := make(map[string]map[string]struct{}, len(rep.Ran))
	for _, f := range rep.Findings {
		if seenKeys[f.CheckID] == nil {
			seenKeys[f.CheckID] = make(map[string]struct{})
		}
		seenKeys[f.CheckID][f.EntityKey] = struct{}{}

		updated, err := updateOpenFinding(ctx, tx, f, rep.GeneratedAt)
		if err != nil {
			return UpsertSummary{}, err
		}
		if updated {
			summary.Updated++
			continue
		}
		if err := insertFinding(ctx, tx, f, rep.GeneratedAt); err != nil {
			return UpsertSummary{}, err
		}
		summary.New++
	}

	for _, checkID := range rep.Ran {
		n, err := resolveMissingFindings(ctx, tx, rep.Node, checkID, seenKeys[checkID], rep.GeneratedAt)
		if err != nil {
			return UpsertSummary{}, err
		}
		summary.Resolved += n
	}

	return summary, nil
}

func updateOpenFinding(ctx context.Context, tx *sql.Tx, f check.Finding, generatedAt time.Time) (bool, error) {
	dataJSON, err := toJSONOrDefault(f.Data, "{}")
	if err != nil {
		return false, fmt.Errorf("store: marshal finding %s data: %w", f.Key(), err)
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE findings
		SET last_seen = ?, seen_count = seen_count + 1, severity = ?, summary = ?, detail = ?, data = ?
		WHERE node = ? AND check_id = ? AND entity_key = ? AND status IN ('open', 'snoozed')`,
		formatTime(generatedAt), string(f.Severity), f.Summary, f.Detail, dataJSON,
		string(f.Node), f.CheckID, f.EntityKey,
	)
	if err != nil {
		return false, fmt.Errorf("store: update finding %s: %w", f.Key(), err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: update finding %s rows affected: %w", f.Key(), err)
	}
	return n > 0, nil
}

func insertFinding(ctx context.Context, tx *sql.Tx, f check.Finding, generatedAt time.Time) error {
	dataJSON, err := toJSONOrDefault(f.Data, "{}")
	if err != nil {
		return fmt.Errorf("store: marshal finding %s data: %w", f.Key(), err)
	}

	firstSeen := formatTime(generatedAt)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO findings (check_id, node, entity_key, severity, tier, summary, detail, data, status, first_seen, last_seen, seen_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?, 1)`,
		f.CheckID, string(f.Node), f.EntityKey, string(f.Severity), string(f.Tier), f.Summary, f.Detail, dataJSON,
		firstSeen, firstSeen,
	); err != nil {
		return fmt.Errorf("store: insert finding %s: %w", f.Key(), err)
	}
	return nil
}

// resolveMissingFindings marks open/snoozed findings for (node, checkID)
// resolved when their entity_key isn't in seenKeys (the keys this report
// reported for that check). It returns how many rows it resolved.
func resolveMissingFindings(ctx context.Context, tx *sql.Tx, node config.Node, checkID string, seenKeys map[string]struct{}, generatedAt time.Time) (int, error) {
	query := `
		UPDATE findings
		SET status = 'resolved', resolved_at = ?
		WHERE node = ? AND check_id = ? AND status IN ('open', 'snoozed')`
	args := []any{formatTime(generatedAt), string(node), checkID}

	if len(seenKeys) > 0 {
		placeholders := make([]string, 0, len(seenKeys))
		for key := range seenKeys {
			placeholders = append(placeholders, "?")
			args = append(args, key)
		}
		query += " AND entity_key NOT IN (" + strings.Join(placeholders, ", ") + ")"
	}

	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("store: resolve findings for check %s: %w", checkID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: resolve findings for check %s rows affected: %w", checkID, err)
	}
	return int(n), nil
}

// OpenFindings returns findings with status open or snoozed for node,
// sorted severity desc, check_id, entity_key.
func (s *Store) OpenFindings(ctx context.Context, node config.Node) (out []StoredFinding, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, check_id, node, entity_key, severity, tier, summary, detail, data, status,
		       first_seen, last_seen, resolved_at, seen_count
		FROM findings
		WHERE node = ? AND status IN ('open', 'snoozed')
		ORDER BY
			CASE severity WHEN 'critical' THEN 3 WHEN 'warn' THEN 2 WHEN 'info' THEN 1 ELSE 0 END DESC,
			check_id, entity_key`,
		string(node),
	)
	if err != nil {
		return nil, fmt.Errorf("store: open findings: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("store: close open findings rows: %w", closeErr))
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
		return nil, fmt.Errorf("store: iterate open findings: %w", rowsErr)
	}
	return out, nil
}

func scanStoredFinding(rows *sql.Rows) (StoredFinding, error) {
	var (
		id                                                                     int64
		checkID, nodeStr, entityKey, severity, tier, summary, detail, dataJSON string
		status, firstSeenStr, lastSeenStr                                      string
		resolvedAtStr                                                          sql.NullString
		seenCount                                                              int
	)
	if err := rows.Scan(&id, &checkID, &nodeStr, &entityKey, &severity, &tier, &summary, &detail, &dataJSON, &status,
		&firstSeenStr, &lastSeenStr, &resolvedAtStr, &seenCount); err != nil {
		return StoredFinding{}, fmt.Errorf("store: scan finding: %w", err)
	}

	firstSeen, err := time.Parse(time.RFC3339, firstSeenStr)
	if err != nil {
		return StoredFinding{}, fmt.Errorf("store: parse finding.first_seen: %w", err)
	}
	lastSeen, err := time.Parse(time.RFC3339, lastSeenStr)
	if err != nil {
		return StoredFinding{}, fmt.Errorf("store: parse finding.last_seen: %w", err)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		return StoredFinding{}, fmt.Errorf("store: unmarshal finding.data: %w", err)
	}

	var resolvedAt *time.Time
	if resolvedAtStr.Valid {
		t, err := time.Parse(time.RFC3339, resolvedAtStr.String)
		if err != nil {
			return StoredFinding{}, fmt.Errorf("store: parse finding.resolved_at: %w", err)
		}
		resolvedAt = &t
	}

	return StoredFinding{
		ID: id,
		Finding: check.Finding{
			CheckID:   checkID,
			Node:      config.Node(nodeStr),
			EntityKey: entityKey,
			Severity:  check.Severity(severity),
			Tier:      check.Tier(tier),
			Summary:   summary,
			Detail:    detail,
			Data:      data,
			FirstSeen: firstSeen,
			LastSeen:  lastSeen,
		},
		Status:     status,
		SeenCount:  seenCount,
		ResolvedAt: resolvedAt,
	}, nil
}
