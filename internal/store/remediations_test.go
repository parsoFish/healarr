package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// insertRawRemediation writes directly to the remediations table, bypassing
// RecordRemediation, so tests can construct rows RecordRemediation would
// never produce (malformed timestamps) to exercise scanRemediation's
// decode errors.
func insertRawRemediation(t *testing.T, s *Store, cols string, args ...any) {
	t.Helper()
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")
	query := fmt.Sprintf("INSERT INTO remediations (%s) VALUES (%s)", cols, placeholders)
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestRecordRemediationWithFindingAndFinishedAt(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	finishedAt := t0.Add(time.Minute)
	findingID := int64(42)

	r := Remediation{
		FindingID: &findingID, Node: config.NodeNAS, Action: "qbit_delete", Tier: "correct",
		Status: "executed", Detail: "removed stalled torrent", DryRun: false,
		CreatedAt: t0, FinishedAt: &finishedAt,
	}
	id, err := s.RecordRemediation(ctx, r)
	if err != nil {
		t.Fatal(err)
	}

	got := mustRecentRemediations(t, s, t0)
	if len(got) != 1 {
		t.Fatalf("recent = %+v, want 1 row", got)
	}
	g := got[0]
	if g.ID != id || g.Node != config.NodeNAS || g.Action != "qbit_delete" || g.Tier != "correct" ||
		g.Status != "executed" || g.Detail != "removed stalled torrent" || g.DryRun {
		t.Fatalf("remediation = %+v, want it to round-trip r=%+v", g, r)
	}
	if g.FindingID == nil || *g.FindingID != findingID {
		t.Fatalf("FindingID = %v, want %d", g.FindingID, findingID)
	}
	if !g.CreatedAt.Equal(t0) {
		t.Fatalf("CreatedAt = %v, want %v", g.CreatedAt, t0)
	}
	if g.FinishedAt == nil || !g.FinishedAt.Equal(finishedAt) {
		t.Fatalf("FinishedAt = %v, want %v", g.FinishedAt, finishedAt)
	}
}

func TestRecordRemediationNilFindingIDAndFinishedAt(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	r := Remediation{
		Node: config.NodePi, Action: "sonarr_nudge", Tier: "nudge", Status: "dry_run",
		Detail: "would search", DryRun: true, CreatedAt: t0,
	}
	if _, err := s.RecordRemediation(ctx, r); err != nil {
		t.Fatal(err)
	}

	got := mustRecentRemediations(t, s, t0)
	if len(got) != 1 || got[0].FindingID != nil || got[0].FinishedAt != nil || !got[0].DryRun {
		t.Fatalf("remediation = %+v, want FindingID/FinishedAt nil and DryRun true", got)
	}
}

func TestRecentRemediationsOrderedAndFiltersBySince(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	older := Remediation{Node: config.NodePi, Action: "a", Tier: "nudge", Status: "executed", CreatedAt: t0}
	newer := Remediation{Node: config.NodePi, Action: "b", Tier: "nudge", Status: "executed", CreatedAt: t0.Add(time.Hour)}
	tooOld := Remediation{Node: config.NodePi, Action: "c", Tier: "nudge", Status: "executed", CreatedAt: t0.Add(-time.Hour)}

	for _, r := range []Remediation{tooOld, older, newer} {
		if _, err := s.RecordRemediation(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	got := mustRecentRemediations(t, s, t0)
	if len(got) != 2 || got[0].Action != "b" || got[1].Action != "a" {
		t.Fatalf("recent since t0 = %+v, want [b, a] newest first (excluding tooOld)", got)
	}
}

func mustRecentRemediations(t *testing.T, s *Store, since time.Time) []Remediation {
	t.Helper()
	got, err := s.RecentRemediations(context.Background(), since)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRecentRemediationsRejectsCorruptRows(t *testing.T) {
	const cols = "node, action, tier, dry_run, status, created_at"
	cases := []struct {
		name string
		args []any
	}{
		{"bad created_at", []any{"pi", "a", "nudge", 1, "executed", "not-a-time"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTemp(t)
			insertRawRemediation(t, s, cols, tc.args...)
			if _, err := s.RecentRemediations(context.Background(), time.Time{}); err == nil {
				t.Fatal("expected error reading a corrupt remediation row")
			}
		})
	}
}

func TestRecentRemediationsRejectsBadFinishedAt(t *testing.T) {
	const cols = "node, action, tier, dry_run, status, created_at, finished_at"
	s := openTemp(t)
	insertRawRemediation(t, s, cols, "pi", "a", "nudge", 1, "executed", "2026-09-18T01:00:00Z", "not-a-time")
	if _, err := s.RecentRemediations(context.Background(), time.Time{}); err == nil {
		t.Fatal("expected error reading a bad finished_at")
	}
}

func TestRecordRemediationErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	r := Remediation{Node: config.NodePi, Action: "a", Tier: "nudge", Status: "executed", CreatedAt: time.Now()}
	if _, err := s.RecordRemediation(context.Background(), r); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestRecentRemediationsErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, err := s.RecentRemediations(context.Background(), time.Time{}); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

// TestRemediationsCreatedIndexExists guards the index RecentRemediations
// relies on to sort/filter by created_at without a full table scan.
func TestRemediationsCreatedIndexExists(t *testing.T) {
	s := openTemp(t)
	var name string
	err := s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'remediations_created'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("remediations_created index missing: %v", err)
	}
}
