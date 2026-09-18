package staleness

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/clients/tautulli"
	"github.com/parsoFish/healarr/internal/config"
)

func stalenessCfg() config.Config {
	return config.Config{Staleness: config.Defaults().Staleness}
}

func checkDeps(cfg config.Config, sc sonarr.Client, rc radarr.Client, tc tautulli.Client) check.Deps {
	return check.Deps{
		Node:     config.NodePi,
		Cfg:      cfg,
		Now:      func() time.Time { return fixedNow },
		Sonarr:   sc,
		Radarr:   rc,
		Tautulli: tc,
	}
}

func TestChecksReturnsStalenessScanRow(t *testing.T) {
	cs := Checks(config.Config{})
	if len(cs) != 1 {
		t.Fatalf("Checks() = %d rows, want 1", len(cs))
	}
	c := cs[0]
	if c.ID != "staleness_scan" {
		t.Errorf("ID = %q, want staleness_scan", c.ID)
	}
	if len(c.Nodes) != 1 || c.Nodes[0] != config.NodePi {
		t.Errorf("Nodes = %v, want [pi]", c.Nodes)
	}
	if c.Tier != check.TierEscalate {
		t.Errorf("Tier = %s, want escalate", c.Tier)
	}
	if c.Cadence != check.Daily {
		t.Errorf("Cadence = %v, want Daily", c.Cadence)
	}
	if c.Run == nil {
		t.Fatal("Run is nil")
	}
}

func TestStalenessScanNotConfigured(t *testing.T) {
	c := Checks(config.Config{})[0]
	_, err := c.Run(context.Background(), checkDeps(stalenessCfg(), nil, nil, nil))
	if !errors.Is(err, check.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

// stalenessScanFixture runs staleness_scan against one candidate series
// (score 75: days 40 + arr 10 + size 15 + age 10), one watchlist series
// (score 60: same but no size points), and one suppressed movie (fully
// watched, monitored & continuing, near-zero score) — one setup shared by
// every assertion below so each stays small enough for gocyclo.
func stalenessScanFixture(t *testing.T) check.Result {
	t.Helper()
	c := Checks(config.Config{})[0]

	sc := &sonarr.Fake{SeriesList: []sonarr.Series{
		{ID: 1, TVDBID: 100, Title: "Candidate Show", Status: "ended", SizeOnDisk: 200_000_000_000, Added: fixedNow.Add(-400 * 24 * time.Hour)},
		{ID: 2, TVDBID: 200, Title: "Watchlist Show", Status: "ended", Added: fixedNow.Add(-400 * 24 * time.Hour)},
	}}
	rc := &radarr.Fake{MovieList: []radarr.Movie{
		{ID: 5, TMDBID: 500, Title: "Suppressed Movie", Monitored: true, Added: fixedNow.Add(-5 * 24 * time.Hour)},
	}}
	tc := &tautulli.Fake{HistoryRows: []tautulli.HistoryRow{
		{MediaType: "movie", Title: "suppressed movie", Date: fixedNow.Add(-1 * 24 * time.Hour), WatchedStatus: 1},
	}}

	res, err := c.Run(context.Background(), checkDeps(stalenessCfg(), sc, rc, tc))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func stalenessFindingsByKey(t *testing.T, res check.Result) map[string]check.Finding {
	t.Helper()
	byKey := map[string]check.Finding{}
	for _, f := range res.Findings {
		byKey[f.EntityKey] = f
	}
	return byKey
}

func TestStalenessScanFindingCount(t *testing.T) {
	res := stalenessScanFixture(t)
	if len(res.Findings) != 2 {
		t.Fatalf("findings = %d, want 2 (candidate + watchlist, suppressed excluded): %+v", len(res.Findings), res.Findings)
	}
}

func TestStalenessScanCandidateFinding(t *testing.T) {
	res := stalenessScanFixture(t)
	cand, ok := stalenessFindingsByKey(t, res)["sonarr:1"]
	if !ok {
		t.Fatalf("missing finding for sonarr:1: %+v", res.Findings)
	}
	if cand.Severity != check.SeverityWarn {
		t.Errorf("severity = %s, want warn", cand.Severity)
	}
	if cand.Tier != check.TierEscalate {
		t.Errorf("tier = %s, want escalate", cand.Tier)
	}
	wantSummary := "Candidate Show stale (score 75): days: 40, size: 15"
	if cand.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", cand.Summary, wantSummary)
	}
}

func TestStalenessScanCandidateFindingData(t *testing.T) {
	res := stalenessScanFixture(t)
	cand := stalenessFindingsByKey(t, res)["sonarr:1"]

	components, ok := cand.Data["components"].(map[string]float64)
	if !ok {
		t.Fatalf("Data[components] not a map[string]float64: %#v", cand.Data["components"])
	}
	if !almostEqual(components["days"], 40) || !almostEqual(components["size"], 15) {
		t.Errorf("components = %+v", components)
	}

	wantData := map[string]any{
		"score":       75.0,
		"sizeBytes":   int64(200_000_000_000),
		"title":       "Candidate Show",
		"kind":        "series",
		"requestedBy": "",
	}
	for key, want := range wantData {
		if got := cand.Data[key]; got != want {
			t.Errorf("Data[%s] = %v, want %v", key, got, want)
		}
	}
}

func TestStalenessScanWatchlistFinding(t *testing.T) {
	res := stalenessScanFixture(t)
	watch, ok := stalenessFindingsByKey(t, res)["sonarr:2"]
	if !ok {
		t.Fatalf("missing finding for sonarr:2: %+v", res.Findings)
	}
	if watch.Severity != check.SeverityInfo {
		t.Errorf("severity = %s, want info", watch.Severity)
	}
}

func TestStalenessScanSuppressedItemHasNoFinding(t *testing.T) {
	res := stalenessScanFixture(t)
	if _, suppressed := stalenessFindingsByKey(t, res)["radarr:5"]; suppressed {
		t.Errorf("suppressed movie should not produce a finding: %+v", res.Findings)
	}
}

func TestStalenessScanMetrics(t *testing.T) {
	res := stalenessScanFixture(t)
	if got := res.Metrics["staleness_candidates"]; got != 1 {
		t.Errorf("staleness_candidates = %v, want 1", got)
	}
	if got := res.Metrics["staleness_watchlist"]; got != 1 {
		t.Errorf("staleness_watchlist = %v, want 1", got)
	}
}

func TestStalenessScanNoItemsProducesZeroMetrics(t *testing.T) {
	c := Checks(config.Config{})[0]
	res, err := c.Run(context.Background(), checkDeps(stalenessCfg(), &sonarr.Fake{}, nil, &tautulli.Fake{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %d, want 0", len(res.Findings))
	}
	if res.Metrics["staleness_candidates"] != 0 || res.Metrics["staleness_watchlist"] != 0 {
		t.Fatalf("metrics = %+v, want zeros", res.Metrics)
	}
}

func TestStalenessScanPropagatesCollectError(t *testing.T) {
	c := Checks(config.Config{})[0]
	boom := errors.New("boom")
	_, err := c.Run(context.Background(), checkDeps(stalenessCfg(), &sonarr.Fake{Err: boom}, nil, &tautulli.Fake{}))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapping %v", err, boom)
	}
}
