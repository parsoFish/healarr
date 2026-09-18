package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// mustSaveReport saves rep and fails the test if SaveReport errors or the
// resulting UpsertSummary doesn't match want.
func mustSaveReport(t *testing.T, s *Store, rep check.Report, want UpsertSummary) {
	t.Helper()
	_, sum, err := s.SaveReport(context.Background(), rep)
	if err != nil {
		t.Fatal(err)
	}
	if sum != want {
		t.Fatalf("summary = %+v, want %+v", sum, want)
	}
}

// mustOpenFindings fetches node's open findings, failing the test on error.
func mustOpenFindings(t *testing.T, s *Store, node config.Node) []StoredFinding {
	t.Helper()
	open, err := s.OpenFindings(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	return open
}

// mustLatestReport fetches node's latest report, failing the test on error
// or when none exists.
func mustLatestReport(t *testing.T, s *Store, node config.Node) check.Report {
	t.Helper()
	rep, ok, err := s.LatestReport(context.Background(), node)
	if err != nil || !ok {
		t.Fatalf("LatestReport: ok=%v err=%v", ok, err)
	}
	return rep
}

// assertOpenFinding checks the identity, severity and dedup bookkeeping of
// a StoredFinding returned by OpenFindings.
func assertOpenFinding(t *testing.T, got StoredFinding, checkID, key string, sev check.Severity, firstSeen, lastSeen time.Time, seenCount int) {
	t.Helper()
	if got.CheckID != checkID || got.EntityKey != key || got.Severity != sev {
		t.Fatalf("finding = %+v, want checkID=%s key=%s severity=%s", got, checkID, key, sev)
	}
	if !got.FirstSeen.Equal(firstSeen) || !got.LastSeen.Equal(lastSeen) || got.SeenCount != seenCount {
		t.Fatalf("finding firstSeen=%v lastSeen=%v seenCount=%d, want %v %v %d",
			got.FirstSeen, got.LastSeen, got.SeenCount, firstSeen, lastSeen, seenCount)
	}
}

func TestSaveReportDedupsAndResolves(t *testing.T) {
	s := openTemp(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	f := func(checkID, key string, sev check.Severity, at time.Time) check.Finding {
		return check.Finding{
			CheckID:   checkID,
			Node:      config.NodePi,
			EntityKey: key,
			Severity:  sev,
			Tier:      check.TierObserve,
			Summary:   "summary for " + key,
			FirstSeen: at,
			LastSeen:  at,
		}
	}

	r1 := check.Report{
		Node:        config.NodePi,
		GeneratedAt: t0,
		Ran:         []string{"a", "b"},
		Metrics:     map[string]float64{"m": 1},
		Findings: []check.Finding{
			f("a", "k1", check.SeverityWarn, t0),
			f("b", "k2", check.SeverityCritical, t0),
		},
	}
	mustSaveReport(t, s, r1, UpsertSummary{New: 2})

	repAfterR1 := mustLatestReport(t, s, config.NodePi)
	if repAfterR1.Metrics["m"] != 1 {
		t.Fatalf("Metrics[m] = %v, want 1", repAfterR1.Metrics["m"])
	}

	t1 := t0.Add(time.Hour)
	r2 := check.Report{
		Node:        config.NodePi,
		GeneratedAt: t1,
		Ran:         []string{"a"},
		Errors:      []check.CheckError{{CheckID: "b", Error: "x"}},
		Findings: []check.Finding{
			// The finding's own LastSeen (t1+1s) deliberately differs from
			// rep.GeneratedAt (t1): the stored last_seen must be stamped
			// from the report time, not the finding's own field.
			f("a", "k1", check.SeverityCritical, t1.Add(time.Second)),
		},
	}
	mustSaveReport(t, s, r2, UpsertSummary{Updated: 1})

	open := mustOpenFindings(t, s, config.NodePi)
	if len(open) != 2 {
		t.Fatalf("open findings = %d, want 2: %+v", len(open), open)
	}
	assertOpenFinding(t, open[0], "a", "k1", check.SeverityCritical, t0, t1, 2)

	r3 := check.Report{
		Node:        config.NodePi,
		GeneratedAt: t1.Add(time.Hour),
		Ran:         []string{"a", "b"},
	}
	mustSaveReport(t, s, r3, UpsertSummary{Resolved: 2})

	open = mustOpenFindings(t, s, config.NodePi)
	if len(open) != 0 {
		t.Fatalf("open findings after resolve = %d, want 0: %+v", len(open), open)
	}

	rep := mustLatestReport(t, s, config.NodePi)
	if !rep.GeneratedAt.Equal(r3.GeneratedAt) {
		t.Fatalf("LatestReport.GeneratedAt = %v, want %v", rep.GeneratedAt, r3.GeneratedAt)
	}
}

// TestSaveReportDedupIsScopedByNode guards against the dedup/resolve match
// collapsing across nodes: two reports for different nodes carrying the
// same check_id+entity_key must produce two independent open rows, and
// resolving on one node must never touch the other node's row.
func TestSaveReportDedupIsScopedByNode(t *testing.T) {
	s := openTemp(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	mkFinding := func(node config.Node) check.Finding {
		return check.Finding{
			CheckID:   "a",
			Node:      node,
			EntityKey: "k1",
			Severity:  check.SeverityWarn,
			Tier:      check.TierObserve,
			Summary:   "s",
			FirstSeen: t0,
			LastSeen:  t0,
		}
	}

	piReport := check.Report{Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{mkFinding(config.NodePi)}}
	mustSaveReport(t, s, piReport, UpsertSummary{New: 1})

	nasReport := check.Report{Node: config.NodeNAS, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{mkFinding(config.NodeNAS)}}
	mustSaveReport(t, s, nasReport, UpsertSummary{New: 1})

	piOpen := mustOpenFindings(t, s, config.NodePi)
	nasOpen := mustOpenFindings(t, s, config.NodeNAS)
	if len(piOpen) != 1 || len(nasOpen) != 1 {
		t.Fatalf("pi open = %d, nas open = %d, want 1 each", len(piOpen), len(nasOpen))
	}

	t1 := t0.Add(time.Hour)
	resolvePi := check.Report{Node: config.NodePi, GeneratedAt: t1, Ran: []string{"a"}}
	mustSaveReport(t, s, resolvePi, UpsertSummary{Resolved: 1})

	piOpen = mustOpenFindings(t, s, config.NodePi)
	nasOpen = mustOpenFindings(t, s, config.NodeNAS)
	if len(piOpen) != 0 {
		t.Fatalf("pi open after resolve = %d, want 0: %+v", len(piOpen), piOpen)
	}
	if len(nasOpen) != 1 {
		t.Fatalf("nas open after pi's resolve = %d, want 1 (nas must be unaffected)", len(nasOpen))
	}
}

func TestLatestReportNoneForNode(t *testing.T) {
	s := openTemp(t)
	_, ok, err := s.LatestReport(context.Background(), config.NodeNAS)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for empty store")
	}
}

func TestOpenFindingsOtherNodeIsolated(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	r := check.Report{
		Node:        config.NodePi,
		GeneratedAt: t0,
		Ran:         []string{"a"},
		Findings: []check.Finding{{
			CheckID:   "a",
			Node:      config.NodePi,
			EntityKey: "k1",
			Severity:  check.SeverityWarn,
			Tier:      check.TierObserve,
			Summary:   "s",
			FirstSeen: t0,
			LastSeen:  t0,
		}},
	}
	if _, _, err := s.SaveReport(ctx, r); err != nil {
		t.Fatal(err)
	}

	open, err := s.OpenFindings(ctx, config.NodeNAS)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("nas open findings = %d, want 0: %+v", len(open), open)
	}

	piOpen, err := s.OpenFindings(ctx, config.NodePi)
	if err != nil {
		t.Fatal(err)
	}
	if len(piOpen) != 1 {
		t.Fatalf("pi open findings = %d, want 1", len(piOpen))
	}
}

// TestSaveReportRejectsUnmarshalableFindingData exercises the marshal-error
// branches in both insertFinding (brand new row) and updateOpenFinding (row
// already open), which a valid check.Finding never reaches in practice.
func TestSaveReportRejectsUnmarshalableFindingData(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	bad := check.Finding{
		CheckID: "a", Node: config.NodePi, EntityKey: "k1",
		Severity: check.SeverityWarn, Tier: check.TierObserve, Summary: "s",
		Data:      map[string]any{"unmarshalable": make(chan int)},
		FirstSeen: t0, LastSeen: t0,
	}

	insertReport := check.Report{Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{bad}}
	if _, _, err := s.SaveReport(ctx, insertReport); err == nil {
		t.Fatal("expected error inserting a finding with unmarshalable data")
	}

	seed := bad
	seed.Data = nil
	seedReport := check.Report{Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a"}, Findings: []check.Finding{seed}}
	if _, _, err := s.SaveReport(ctx, seedReport); err != nil {
		t.Fatal(err)
	}

	updateBad := bad
	updateBad.LastSeen = t0.Add(time.Hour)
	updateReport := check.Report{Node: config.NodePi, GeneratedAt: updateBad.LastSeen, Ran: []string{"a"}, Findings: []check.Finding{updateBad}}
	if _, _, err := s.SaveReport(ctx, updateReport); err == nil {
		t.Fatal("expected error updating a finding with unmarshalable data")
	}
}

// insertRawFinding writes directly to the findings table, bypassing
// SaveReport, so tests can construct rows SaveReport would never produce
// (malformed JSON/timestamps) to exercise OpenFindings' decode errors.
func insertRawFinding(t *testing.T, s *Store, cols string, args ...any) {
	t.Helper()
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")
	query := fmt.Sprintf("INSERT INTO findings (%s) VALUES (%s)", cols, placeholders)
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestOpenFindingsRejectsCorruptRows(t *testing.T) {
	const cols = "check_id, node, entity_key, severity, tier, summary, detail, data, status, first_seen, last_seen, seen_count"
	cases := []struct {
		name string
		args []any
	}{
		{"bad data json", []any{"a", "pi", "k1", "warn", "observe", "s", "", "{not json", "open", "2026-09-18T01:00:00Z", "2026-09-18T01:00:00Z", 1}},
		{"bad first_seen", []any{"a", "pi", "k1", "warn", "observe", "s", "", "{}", "open", "not-a-time", "2026-09-18T01:00:00Z", 1}},
		{"bad last_seen", []any{"a", "pi", "k1", "warn", "observe", "s", "", "{}", "open", "2026-09-18T01:00:00Z", "not-a-time", 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTemp(t)
			insertRawFinding(t, s, cols, tc.args...)
			if _, err := s.OpenFindings(context.Background(), config.NodePi); err == nil {
				t.Fatal("expected error reading a corrupt row")
			}
		})
	}
}

func TestOpenFindingsRejectsBadResolvedAt(t *testing.T) {
	const cols = "check_id, node, entity_key, severity, tier, summary, detail, data, status, first_seen, last_seen, resolved_at, seen_count"
	s := openTemp(t)
	insertRawFinding(t, s, cols, "a", "pi", "k1", "warn", "observe", "s", "", "{}", "open",
		"2026-09-18T01:00:00Z", "2026-09-18T01:00:00Z", "not-a-time", 1)
	if _, err := s.OpenFindings(context.Background(), config.NodePi); err == nil {
		t.Fatal("expected error parsing a bad resolved_at")
	}
}

func TestOpenFindingsParsesResolvedAtWhenPresent(t *testing.T) {
	const cols = "check_id, node, entity_key, severity, tier, summary, detail, data, status, first_seen, last_seen, resolved_at, seen_count"
	s := openTemp(t)
	insertRawFinding(t, s, cols, "a", "pi", "k1", "warn", "observe", "s", "", "{}", "open",
		"2026-09-18T01:00:00Z", "2026-09-18T01:00:00Z", "2026-09-18T02:00:00Z", 1)

	open, err := s.OpenFindings(context.Background(), config.NodePi)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ResolvedAt == nil {
		t.Fatalf("expected one finding with ResolvedAt set, got %+v", open)
	}
}

func TestLatestReportRejectsCorruptRows(t *testing.T) {
	const cols = "node, generated_at, checks_run, checks_failed, findings_count, ran, skipped, errors, metrics"
	cases := []struct {
		name string
		args []any
	}{
		{"bad generated_at", []any{"pi", "not-a-time", 1, 0, 0, "[]", "[]", "[]", "{}"}},
		{"bad ran json", []any{"pi", "2026-09-18T01:00:00Z", 1, 0, 0, "not json", "[]", "[]", "{}"}},
		{"bad skipped json", []any{"pi", "2026-09-18T01:00:00Z", 1, 0, 0, "[]", "not json", "[]", "{}"}},
		{"bad errors json", []any{"pi", "2026-09-18T01:00:00Z", 1, 0, 0, "[]", "[]", "not json", "{}"}},
		{"bad metrics json", []any{"pi", "2026-09-18T01:00:00Z", 1, 0, 0, "[]", "[]", "[]", "not json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTemp(t)
			placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(tc.args)), ", ")
			query := fmt.Sprintf("INSERT INTO reports (%s) VALUES (%s)", cols, placeholders)
			if _, err := s.db.Exec(query, tc.args...); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.LatestReport(context.Background(), config.NodePi); err == nil {
				t.Fatal("expected error reading a corrupt report row")
			}
		})
	}
}
