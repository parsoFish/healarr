package store

import (
	"context"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

func histFinding(checkID, key string, at time.Time) check.Finding {
	return check.Finding{
		CheckID:   checkID,
		Node:      config.NodePi,
		EntityKey: key,
		Severity:  check.SeverityWarn,
		Tier:      check.TierObserve,
		Summary:   "s",
		FirstSeen: at,
		LastSeen:  at,
	}
}

// TestFindingHistoryIncludesEveryStatus covers the resolved+open+snoozed
// contract: an entity that's still open, one that's snoozed, and one
// that's since resolved must all appear.
func TestFindingHistoryIncludesEveryStatus(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	r1 := check.Report{
		Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a", "b", "c"},
		Findings: []check.Finding{histFinding("a", "e1", t0), histFinding("b", "e2", t0), histFinding("c", "e3", t0)},
	}
	if _, _, err := s.SaveReport(ctx, r1); err != nil {
		t.Fatal(err)
	}

	until := t0.Add(2 * time.Hour)
	if err := s.SnoozeEntity(ctx, "e2", until); err != nil {
		t.Fatal(err)
	}

	// e1 and e2 keep firing (e2 stays snoozed, since t1 < until); e3 isn't
	// reported again even though check "c" ran, so it resolves.
	t1 := t0.Add(30 * time.Minute)
	r2 := check.Report{
		Node: config.NodePi, GeneratedAt: t1, Ran: []string{"a", "b", "c"},
		Findings: []check.Finding{histFinding("a", "e1", t1), histFinding("b", "e2", t1)},
	}
	if _, _, err := s.SaveReport(ctx, r2); err != nil {
		t.Fatal(err)
	}

	got, err := s.FindingHistory(ctx, config.NodePi, t0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("history = %+v, want 3 rows", got)
	}

	byKey := map[string]StoredFinding{}
	for _, f := range got {
		byKey[f.EntityKey] = f
	}
	if byKey["e1"].Status != "open" {
		t.Fatalf("e1 status = %q, want open", byKey["e1"].Status)
	}
	if byKey["e2"].Status != "snoozed" || byKey["e2"].SnoozeUntil == nil || !byKey["e2"].SnoozeUntil.Equal(until) {
		t.Fatalf("e2 = %+v, want status=snoozed SnoozeUntil=%v", byKey["e2"], until)
	}
	if byKey["e3"].Status != "resolved" || byKey["e3"].ResolvedAt == nil {
		t.Fatalf("e3 = %+v, want status=resolved with ResolvedAt set", byKey["e3"])
	}

	// Newest last_seen (e1/e2 at t1) must sort before the untouched e3 (t0).
	if !got[len(got)-1].LastSeen.Equal(t0) || got[len(got)-1].EntityKey != "e3" {
		t.Fatalf("last row = %+v, want e3 at t0 (oldest last_seen sorts last)", got[len(got)-1])
	}
}

func TestFindingHistoryFiltersBySince(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	early := check.Report{Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{histFinding("a", "e1", t0)}}
	if _, _, err := s.SaveReport(ctx, early); err != nil {
		t.Fatal(err)
	}
	t2 := t0.Add(2 * time.Hour)
	later := check.Report{Node: config.NodePi, GeneratedAt: t2, Ran: []string{"b"}, Findings: []check.Finding{histFinding("b", "e2", t2)}}
	if _, _, err := s.SaveReport(ctx, later); err != nil {
		t.Fatal(err)
	}

	since := t0.Add(time.Hour)
	got, err := s.FindingHistory(ctx, config.NodePi, since, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EntityKey != "e2" {
		t.Fatalf("history since %v = %+v, want only e2", since, got)
	}
}

func TestFindingHistoryRespectsLimitAndOrder(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	for i, checkID := range []string{"a", "b", "c"} {
		at := t0.Add(time.Duration(i) * time.Hour)
		rep := check.Report{Node: config.NodePi, GeneratedAt: at, Ran: []string{checkID}, Findings: []check.Finding{histFinding(checkID, checkID, at)}}
		if _, _, err := s.SaveReport(ctx, rep); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.FindingHistory(ctx, config.NodePi, t0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].EntityKey != "c" || got[1].EntityKey != "b" {
		t.Fatalf("history limit=2 = %+v, want [c, b] newest first", got)
	}
}

func TestFindingHistoryScopedByNode(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	piFinding := histFinding("a", "e1", t0)
	nasFinding := histFinding("a", "e1", t0)
	nasFinding.Node = config.NodeNAS

	if _, _, err := s.SaveReport(ctx, check.Report{Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{piFinding}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SaveReport(ctx, check.Report{Node: config.NodeNAS, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{nasFinding}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.FindingHistory(ctx, config.NodeNAS, t0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Node != config.NodeNAS {
		t.Fatalf("history for nas = %+v, want exactly 1 nas row", got)
	}
}

func TestFindingHistoryErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, err := s.FindingHistory(context.Background(), config.NodePi, time.Time{}, 10); err == nil {
		t.Fatal("expected error from a closed store")
	}
}
