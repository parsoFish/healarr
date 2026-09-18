package store

import (
	"context"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// snoozeTestFinding builds the single-finding report/finding pair the two
// snooze-dedup tests below share, differing only in GeneratedAt.
func snoozeTestFinding(at time.Time) check.Finding {
	return check.Finding{
		CheckID:   "a",
		Node:      config.NodePi,
		EntityKey: "sonarr:12",
		Severity:  check.SeverityWarn,
		Tier:      check.TierObserve,
		Summary:   "s",
		FirstSeen: at,
		LastSeen:  at,
	}
}

// TestUpsertFindingsKeepsSnoozedWhileActive covers the dedup note: a
// matched row whose entity is snoozed must stay snoozed (not flip back to
// open) as long as the report's GeneratedAt is still before snooze_until.
func TestUpsertFindingsKeepsSnoozedWhileActive(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	r1 := check.Report{Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{snoozeTestFinding(t0)}}
	mustSaveReport(t, s, r1, UpsertSummary{New: 1})

	until := t0.Add(24 * time.Hour)
	if err := s.SnoozeEntity(ctx, "sonarr:12", until); err != nil {
		t.Fatal(err)
	}

	open := mustOpenFindings(t, s, config.NodePi)
	if len(open) != 1 || open[0].Status != "snoozed" || open[0].SnoozeUntil == nil || !open[0].SnoozeUntil.Equal(until) {
		t.Fatalf("after snooze, open = %+v, want one snoozed row with SnoozeUntil=%v", open, until)
	}

	t1 := t0.Add(time.Hour) // still well before `until`
	r2 := check.Report{Node: config.NodePi, GeneratedAt: t1, Ran: []string{"a"}, Findings: []check.Finding{snoozeTestFinding(t1)}}
	mustSaveReport(t, s, r2, UpsertSummary{Updated: 1})

	open = mustOpenFindings(t, s, config.NodePi)
	if len(open) != 1 || open[0].Status != "snoozed" || open[0].SnoozeUntil == nil || !open[0].SnoozeUntil.Equal(until) {
		t.Fatalf("after re-report while snoozed, open = %+v, want still snoozed until %v", open, until)
	}
	if open[0].SeenCount != 2 {
		t.Fatalf("SeenCount = %d, want 2 (the snoozed row must still be refreshed)", open[0].SeenCount)
	}
}

// TestUpsertFindingsReopensAfterSnoozeExpires covers the other half of the
// dedup note: once GeneratedAt reaches or passes snooze_until, the matched
// row must flip back to open and its snooze_until must clear.
func TestUpsertFindingsReopensAfterSnoozeExpires(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	r1 := check.Report{Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{snoozeTestFinding(t0)}}
	mustSaveReport(t, s, r1, UpsertSummary{New: 1})

	until := t0.Add(time.Hour)
	if err := s.SnoozeEntity(ctx, "sonarr:12", until); err != nil {
		t.Fatal(err)
	}

	// GeneratedAt == until: the snooze has passed, not "still active".
	t1 := until
	r2 := check.Report{Node: config.NodePi, GeneratedAt: t1, Ran: []string{"a"}, Findings: []check.Finding{snoozeTestFinding(t1)}}
	mustSaveReport(t, s, r2, UpsertSummary{Updated: 1})

	open := mustOpenFindings(t, s, config.NodePi)
	if len(open) != 1 || open[0].Status != "open" || open[0].SnoozeUntil != nil {
		t.Fatalf("after snooze expiry, open = %+v, want status=open with SnoozeUntil=nil", open)
	}
}
