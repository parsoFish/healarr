package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// UpsertSummary counts what SaveReport did to the findings table.
type UpsertSummary struct {
	New, Updated, Resolved int
}

// SaveReport inserts the report row and upserts its findings in one
// transaction. It returns the stored report id and an UpsertSummary.
func (s *Store) SaveReport(ctx context.Context, rep check.Report) (id int64, summary UpsertSummary, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, UpsertSummary{}, fmt.Errorf("store: begin save report: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("store: rollback save report: %w", rbErr))
		}
	}()

	id, err = insertReport(ctx, tx, rep)
	if err != nil {
		return 0, UpsertSummary{}, err
	}

	summary, err = upsertFindings(ctx, tx, rep)
	if err != nil {
		return 0, UpsertSummary{}, err
	}

	if err = tx.Commit(); err != nil {
		return 0, UpsertSummary{}, fmt.Errorf("store: commit save report: %w", err)
	}
	committed = true

	return id, summary, nil
}

func insertReport(ctx context.Context, tx *sql.Tx, rep check.Report) (int64, error) {
	ran, err := toJSONOrDefault(rep.Ran, "[]")
	if err != nil {
		return 0, fmt.Errorf("store: marshal report.ran: %w", err)
	}
	skipped, err := toJSONOrDefault(rep.Skipped, "[]")
	if err != nil {
		return 0, fmt.Errorf("store: marshal report.skipped: %w", err)
	}
	checkErrors, err := toJSONOrDefault(rep.Errors, "[]")
	if err != nil {
		return 0, fmt.Errorf("store: marshal report.errors: %w", err)
	}
	metrics, err := toJSONOrDefault(rep.Metrics, "{}")
	if err != nil {
		return 0, fmt.Errorf("store: marshal report.metrics: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO reports (node, generated_at, checks_run, checks_failed, findings_count, ran, skipped, errors, metrics)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(rep.Node), formatTime(rep.GeneratedAt), rep.ChecksRun, rep.ChecksFailed, len(rep.Findings),
		ran, skipped, checkErrors, metrics,
	)
	if err != nil {
		return 0, fmt.Errorf("store: insert report: %w", err)
	}

	reportID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: report id: %w", err)
	}
	return reportID, nil
}

// LatestReport returns the most recent report for node (findings not
// loaded; Metrics loaded). ok=false when none.
func (s *Store) LatestReport(ctx context.Context, node config.Node) (check.Report, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT node, generated_at, checks_run, checks_failed, ran, skipped, errors, metrics
		FROM reports
		WHERE node = ?
		ORDER BY generated_at DESC, id DESC
		LIMIT 1`,
		string(node),
	)

	var (
		nodeStr                                       string
		generatedAtStr                                string
		checksRun, checksFailed                       int
		ranJSON, skippedJSON, errorsJSON, metricsJSON string
	)
	if err := row.Scan(&nodeStr, &generatedAtStr, &checksRun, &checksFailed, &ranJSON, &skippedJSON, &errorsJSON, &metricsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return check.Report{}, false, nil
		}
		return check.Report{}, false, fmt.Errorf("store: latest report: %w", err)
	}

	rep, err := decodeReport(nodeStr, generatedAtStr, checksRun, checksFailed, ranJSON, skippedJSON, errorsJSON, metricsJSON)
	if err != nil {
		return check.Report{}, false, err
	}
	return rep, true, nil
}

func decodeReport(nodeStr, generatedAtStr string, checksRun, checksFailed int, ranJSON, skippedJSON, errorsJSON, metricsJSON string) (check.Report, error) {
	generatedAt, err := time.Parse(time.RFC3339, generatedAtStr)
	if err != nil {
		return check.Report{}, fmt.Errorf("store: parse report.generated_at: %w", err)
	}

	var ran, skipped []string
	if err := json.Unmarshal([]byte(ranJSON), &ran); err != nil {
		return check.Report{}, fmt.Errorf("store: unmarshal report.ran: %w", err)
	}
	if err := json.Unmarshal([]byte(skippedJSON), &skipped); err != nil {
		return check.Report{}, fmt.Errorf("store: unmarshal report.skipped: %w", err)
	}

	var checkErrors []check.CheckError
	if err := json.Unmarshal([]byte(errorsJSON), &checkErrors); err != nil {
		return check.Report{}, fmt.Errorf("store: unmarshal report.errors: %w", err)
	}

	var metrics map[string]float64
	if err := json.Unmarshal([]byte(metricsJSON), &metrics); err != nil {
		return check.Report{}, fmt.Errorf("store: unmarshal report.metrics: %w", err)
	}

	return check.Report{
		Node:         config.Node(nodeStr),
		GeneratedAt:  generatedAt,
		Ran:          ran,
		Skipped:      skipped,
		Errors:       checkErrors,
		Metrics:      metrics,
		ChecksRun:    checksRun,
		ChecksFailed: checksFailed,
	}, nil
}

// formatTime renders t as an RFC 3339 UTC string for storage.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// toJSONOrDefault marshals v to JSON, substituting empty for a nil slice
// or map (json.Marshal would otherwise produce the string "null", and the
// schema's columns are NOT NULL with '[]'/'{}' defaults).
func toJSONOrDefault(v any, empty string) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	if string(b) == "null" {
		return empty, nil
	}
	return string(b), nil
}
