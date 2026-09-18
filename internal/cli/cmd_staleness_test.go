package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

// stalenessTestCfg is a config with Sonarr and Tautulli configured (so
// buildCheckDeps builds real clients from the fakes) and the C6 default
// staleness weights.
func stalenessTestCfg() config.Config {
	return config.Config{
		Node: config.NodePi,
		Services: config.Services{
			Sonarr:   config.Service{URL: "http://sonarr.example.com"},
			Tautulli: config.Service{URL: "http://tautulli.example.com"},
		},
		Staleness: config.Defaults().Staleness,
		Checks:    config.Checks{Timeout: testCheckTimeout},
	}
}

func TestStalenessScoreTableListsCandidatesSortedDescending(t *testing.T) {
	deps, fk := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return stalenessTestCfg(), config.Secrets{}, nil
	}
	now := time.Now()
	fk.Sonarr.SeriesList = []sonarr.Series{
		// Never watched, ended, added long ago, large on disk: well over
		// the candidate threshold.
		{ID: 1, Title: "High Score Show", Status: "ended", Added: now.Add(-400 * 24 * time.Hour), SizeOnDisk: 200_000_000_000},
		// Continuing and monitored, added recently: well under the
		// watchlist threshold, must be filtered out of the default view.
		{ID: 2, Title: "Low Score Show", Status: "continuing", Monitored: true, Added: now.Add(-1 * 24 * time.Hour)},
	}

	out := runCLI(t, deps, "staleness", "score")

	if !strings.Contains(out, "High Score Show") {
		t.Errorf("expected High Score Show in output, got %q", out)
	}
	if strings.Contains(out, "Low Score Show") {
		t.Errorf("expected Low Score Show to be filtered out, got %q", out)
	}
	if !strings.Contains(out, "sonarr:1") {
		t.Errorf("expected entity key sonarr:1 in output, got %q", out)
	}
}

func TestStalenessScoreJSONShape(t *testing.T) {
	deps, fk := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return stalenessTestCfg(), config.Secrets{}, nil
	}
	fk.Sonarr.SeriesList = []sonarr.Series{
		{ID: 1, Title: "High Score Show", Status: "ended", Added: time.Now().Add(-400 * 24 * time.Hour), SizeOnDisk: 200_000_000_000},
	}

	out := runCLI(t, deps, "--json", "staleness", "score")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1: %s", len(rows), out)
	}
	for _, key := range []string{"EntityKey", "Title", "Score", "Band", "Top"} {
		if _, ok := rows[0][key]; !ok {
			t.Errorf("missing key %q in %v", key, rows[0])
		}
	}
	if rows[0]["Band"] != "candidate" {
		t.Errorf("Band = %v, want candidate", rows[0]["Band"])
	}
}

func TestStalenessScoreMinFlagOverridesDefault(t *testing.T) {
	deps, fk := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return stalenessTestCfg(), config.Secrets{}, nil
	}
	fk.Sonarr.SeriesList = []sonarr.Series{
		{ID: 1, Title: "High Score Show", Status: "ended", Added: time.Now().Add(-400 * 24 * time.Hour), SizeOnDisk: 200_000_000_000},
	}

	out := runCLI(t, deps, "--json", "staleness", "score", "--min", "1000")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 with --min 1000: %s", len(rows), out)
	}
}

func TestStalenessScoreNotConfiguredErrors(t *testing.T) {
	deps, _ := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodePi, Checks: config.Checks{Timeout: testCheckTimeout}}, config.Secrets{}, nil
	}
	if _, err := runCLIErr(t, deps, "staleness", "score"); err == nil {
		t.Fatal("expected an error when neither sonarr/radarr nor tautulli is configured")
	}
}
