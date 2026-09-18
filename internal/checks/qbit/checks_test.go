package qbit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

// fixedNow is the deterministic clock every test in this package uses.
var fixedNow = time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)

// wantFinding is what a table row asserts about one produced finding: key
// and severity only (Detail/Data are asserted separately where a test pins
// exact values, e.g. the fake .exe episode fixture).
type wantFinding struct {
	key string
	sev check.Severity
}

// errAny marks a table row that expects some error without pinning the
// exact sentinel.
var errAny = errors.New("qbit_test: any error")

// testChecksCfg returns the family's thresholds set to the same values as
// config's own defaults, so tests read like production behaviour.
func testChecksCfg() config.Checks {
	return config.Checks{
		QBitStalledAfter:          time.Hour,
		CompletedNotImportedAfter: 30 * time.Minute,
		ArrHistoryWindow:          7 * 24 * time.Hour,
		WrongFileExts:             []string{".exe", ".scr", ".bat", ".lnk", ".msi"},
		TVCategories:              []string{"tv"},
		SeededMinAge:              24 * time.Hour,
	}
}

// baseDeps builds a check.Deps factory for table-driven tests: fixed node
// and clock, the given checks config and clients.
func baseDeps(cfg config.Checks, qbit qbittorrent.Client, snr sonarr.Client, rdr radarr.Client) func() check.Deps {
	return func() check.Deps {
		return check.Deps{
			Node:   config.NodeNAS,
			Cfg:    config.Config{Checks: cfg},
			Now:    func() time.Time { return fixedNow },
			QBit:   qbit,
			Sonarr: snr,
			Radarr: rdr,
		}
	}
}

// run executes c.Run against deps(), asserts the error against wantErr
// (errors.Is for a sentinel, non-nil for errAny, nil otherwise) and, when
// no error was expected, that the findings' keys+severities set-equal want.
func run(t *testing.T, c check.Check, deps func() check.Deps, want []wantFinding, wantErr error) check.Result {
	t.Helper()
	res, err := c.Run(context.Background(), deps())
	switch {
	case wantErr == errAny:
		if err == nil {
			t.Fatalf("expected an error, got nil")
		}
	case wantErr != nil:
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want errors.Is(%v)", err, wantErr)
		}
	default:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wantErr == nil {
		assertFindings(t, res.Findings, want)
	}
	return res
}

// TestChecksCatalogue pins the family's catalogue metadata: ids, node
// placement, tier and cadence, per the brief.
func TestChecksCatalogue(t *testing.T) {
	want := []struct {
		id      string
		nodes   []config.Node
		tier    check.Tier
		cadence time.Duration
	}{
		{stalledErroredID, check.NASOnly, check.TierNudge, check.Every15m},
		{notImportedID, check.NASOnly, check.TierCorrect, check.Every15m},
		{wrongFileTypeID, check.NASOnly, check.TierCorrect, check.Every15m},
		{seededDoneID, check.NASOnly, check.TierNudge, check.Daily},
	}
	got := Checks(config.Config{})
	if len(got) != len(want) {
		t.Fatalf("Checks() returned %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		c := got[i]
		if c.ID != w.id {
			t.Errorf("row %d: ID = %q, want %q", i, c.ID, w.id)
		}
		if len(c.Nodes) != len(w.nodes) || c.Nodes[0] != w.nodes[0] {
			t.Errorf("row %d (%s): Nodes = %v, want %v", i, c.ID, c.Nodes, w.nodes)
		}
		if c.Tier != w.tier {
			t.Errorf("row %d (%s): Tier = %s, want %s", i, c.ID, c.Tier, w.tier)
		}
		if c.Cadence != w.cadence {
			t.Errorf("row %d (%s): Cadence = %v, want %v", i, c.ID, c.Cadence, w.cadence)
		}
		if c.Run == nil {
			t.Errorf("row %d (%s): Run is nil", i, c.ID)
		}
	}
}

func assertFindings(t *testing.T, got []check.Finding, want []wantFinding) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("findings = %+v, want %+v", got, want)
	}
	gotSet := make(map[string]check.Severity, len(got))
	for _, f := range got {
		gotSet[f.EntityKey] = f.Severity
	}
	for _, w := range want {
		sev, ok := gotSet[w.key]
		if !ok {
			t.Fatalf("missing finding for key %s; got %+v", w.key, got)
		}
		if sev != w.sev {
			t.Fatalf("key %s severity = %s, want %s", w.key, sev, w.sev)
		}
	}
}
