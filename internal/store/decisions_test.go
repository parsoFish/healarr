package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// insertRawDecision writes directly to the decisions table, bypassing
// CreateDecision, so tests can construct rows CreateDecision would never
// produce (malformed timestamps) to exercise scanDecisionRow's decode
// errors.
func insertRawDecision(t *testing.T, s *Store, cols string, args ...any) {
	t.Helper()
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")
	query := fmt.Sprintf("INSERT INTO decisions (%s) VALUES (%s)", cols, placeholders)
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestCreateDecisionAndDecisionByID(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	id, err := s.CreateDecision(ctx, "sonarr:12", "keep", at)
	if err != nil {
		t.Fatal(err)
	}

	d, ok, err := s.DecisionByID(ctx, id)
	if err != nil || !ok {
		t.Fatalf("DecisionByID: ok=%v err=%v", ok, err)
	}
	if d.ID != id || d.EntityKey != "sonarr:12" || d.Kind != "keep" || d.Status != "pending" {
		t.Fatalf("decision = %+v, want id=%d entityKey=sonarr:12 kind=keep status=pending", d, id)
	}
	if !d.RequestedAt.Equal(at) {
		t.Fatalf("RequestedAt = %v, want %v", d.RequestedAt, at)
	}
	if d.ExecutedAt != nil || d.SnoozeUntil != nil || d.Error != "" {
		t.Fatalf("fresh decision = %+v, want ExecutedAt/SnoozeUntil nil and Error empty", d)
	}
}

func TestDecisionByIDNotFound(t *testing.T) {
	s := openTemp(t)
	_, ok, err := s.DecisionByID(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for a missing decision")
	}
}

func TestPendingDecisionsOrderedAndExcludesResolved(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	id1, err := s.CreateDecision(ctx, "sonarr:1", "keep", t0)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.CreateDecision(ctx, "sonarr:2", "keep", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingDecisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].ID != id1 || pending[1].ID != id2 {
		t.Fatalf("pending = %+v, want [%d, %d] oldest first", pending, id1, id2)
	}

	if err := s.MarkDecision(ctx, id1, "executed", t0.Add(2*time.Minute), nil); err != nil {
		t.Fatal(err)
	}

	pending, err = s.PendingDecisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != id2 {
		t.Fatalf("pending after resolving id1 = %+v, want only [%d]", pending, id2)
	}
}

func TestMarkDecisionExecutedAllowsNilCause(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	id, err := s.CreateDecision(ctx, "sonarr:1", "keep", t0)
	if err != nil {
		t.Fatal(err)
	}

	at := t0.Add(time.Minute)
	if err := s.MarkDecision(ctx, id, "executed", at, nil); err != nil {
		t.Fatal(err)
	}

	d, ok, err := s.DecisionByID(ctx, id)
	if err != nil || !ok {
		t.Fatalf("DecisionByID: ok=%v err=%v", ok, err)
	}
	if d.Status != "executed" || d.ExecutedAt == nil || !d.ExecutedAt.Equal(at) || d.Error != "" {
		t.Fatalf("decision = %+v, want status=executed ExecutedAt=%v Error=\"\"", d, at)
	}
}

func TestMarkDecisionFailedAndBlockedRequireCause(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	for _, status := range []string{"failed", "blocked"} {
		id, err := s.CreateDecision(ctx, "sonarr:1", "keep", t0)
		if err != nil {
			t.Fatal(err)
		}

		if err := s.MarkDecision(ctx, id, status, t0, nil); !errors.Is(err, ErrBadDecisionFailure) {
			t.Fatalf("MarkDecision(%s) with nil cause = %v, want ErrBadDecisionFailure", status, err)
		}

		cause := errors.New("boom: " + status)
		at := t0.Add(time.Minute)
		if err := s.MarkDecision(ctx, id, status, at, cause); err != nil {
			t.Fatalf("MarkDecision(%s) with cause: %v", status, err)
		}

		d, ok, err := s.DecisionByID(ctx, id)
		if err != nil || !ok {
			t.Fatalf("DecisionByID: ok=%v err=%v", ok, err)
		}
		if d.Status != status || d.Error != cause.Error() || d.ExecutedAt == nil || !d.ExecutedAt.Equal(at) {
			t.Fatalf("decision = %+v, want status=%s Error=%q ExecutedAt=%v", d, status, cause.Error(), at)
		}
	}
}

func TestMarkDecisionRejectsBadStatus(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	id, err := s.CreateDecision(ctx, "sonarr:1", "keep", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"pending", "bogus", ""} {
		if err := s.MarkDecision(ctx, id, bad, time.Now(), nil); !errors.Is(err, ErrBadDecisionStatus) {
			t.Fatalf("MarkDecision(%q) = %v, want ErrBadDecisionStatus", bad, err)
		}
	}
}

func TestMarkDecisionNotFound(t *testing.T) {
	s := openTemp(t)
	err := s.MarkDecision(context.Background(), 999, "executed", time.Now(), nil)
	if !errors.Is(err, ErrDecisionNotFound) {
		t.Fatalf("MarkDecision on missing id = %v, want ErrDecisionNotFound", err)
	}
}

func TestSnoozeEntitySetsFindingsAndSnoozedUntil(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	_, ok, err := s.SnoozedUntil(ctx, "sonarr:12")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for an entity nothing has snoozed")
	}

	// SnoozeEntity on an entity with no findings must not error.
	until := time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)
	if err := s.SnoozeEntity(ctx, "sonarr:12", until); err != nil {
		t.Fatalf("SnoozeEntity on an entity with no findings: %v", err)
	}

	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	rep := check.Report{Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{snoozeTestFinding(t0)}}
	if _, _, err := s.SaveReport(ctx, rep); err != nil {
		t.Fatal(err)
	}

	if err := s.SnoozeEntity(ctx, "sonarr:12", until); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.SnoozedUntil(ctx, "sonarr:12")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !got.Equal(until) {
		t.Fatalf("SnoozedUntil = %v, %v, want %v, true", got, ok, until)
	}
}

func TestDecisionByIDRejectsCorruptRows(t *testing.T) {
	const cols = "entity_key, kind, status, requested_at"
	cases := []struct {
		name string
		args []any
	}{
		{"bad requested_at", []any{"sonarr:1", "keep", "pending", "not-a-time"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTemp(t)
			insertRawDecision(t, s, cols, tc.args...)
			if _, _, err := s.DecisionByID(context.Background(), 1); err == nil {
				t.Fatal("expected error reading a corrupt decision row")
			}
		})
	}
}

func TestDecisionByIDRejectsCorruptOptionalTimestamps(t *testing.T) {
	const cols = "entity_key, kind, status, snooze_until, requested_at, executed_at"
	cases := []struct {
		name string
		args []any
	}{
		{"bad snooze_until", []any{"sonarr:1", "keep", "snoozed", "not-a-time", "2026-09-18T01:00:00Z", nil}},
		{"bad executed_at", []any{"sonarr:1", "keep", "executed", nil, "2026-09-18T01:00:00Z", "not-a-time"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTemp(t)
			insertRawDecision(t, s, cols, tc.args...)
			if _, _, err := s.DecisionByID(context.Background(), 1); err == nil {
				t.Fatal("expected error reading a corrupt decision row")
			}
		})
	}
}

func TestCreateDecisionErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, err := s.CreateDecision(context.Background(), "sonarr:1", "keep", time.Now()); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestPendingDecisionsErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, err := s.PendingDecisions(context.Background()); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestDecisionByIDErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, _, err := s.DecisionByID(context.Background(), 1); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestMarkDecisionErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if err := s.MarkDecision(context.Background(), 1, "executed", time.Now(), nil); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestSnoozeEntityErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if err := s.SnoozeEntity(context.Background(), "sonarr:1", time.Now()); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestSnoozedUntilErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, _, err := s.SnoozedUntil(context.Background(), "sonarr:1"); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

// TestDecisionsStatusIndexExists guards the index PendingDecisions relies
// on to filter status='pending' without a full table scan.
func TestDecisionsStatusIndexExists(t *testing.T) {
	s := openTemp(t)
	var name string
	err := s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'decisions_status'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("decisions_status index missing: %v", err)
	}
}
