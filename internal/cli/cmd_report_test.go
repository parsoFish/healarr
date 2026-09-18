package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

func TestReportGenerateDryRunRendersInMemoryDigest(t *testing.T) {
	deps, _ := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodePi, Checks: config.Checks{Timeout: testCheckTimeout}}, config.Secrets{}, nil
	}
	out := runCLI(t, deps, "--dry-run", "report", "generate")
	if !strings.Contains(out, "healarr digest") {
		t.Errorf("expected a rendered digest, got %q", out)
	}
}

func TestReportGenerateDryRunJSON(t *testing.T) {
	deps, _ := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodePi, Checks: config.Checks{Timeout: testCheckTimeout}}, config.Secrets{}, nil
	}
	out := runCLI(t, deps, "--dry-run", "--json", "report", "generate")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	// notify.DigestInput is json-tagged in the same camelCase as
	// check.Finding, so --json output is one consistent shape.
	if got["node"] != "pi" {
		t.Errorf("node = %v, want pi", got["node"])
	}
	for _, key := range []string{"generatedAt", "findings", "errors", "skipped", "checksRun", "metrics"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing json key %q in %v", key, got)
		}
	}
	for _, untagged := range []string{"Node", "GeneratedAt", "ChecksRun"} {
		if _, ok := got[untagged]; ok {
			t.Errorf("untagged Go field name %q leaked into the JSON", untagged)
		}
	}
}

// TestReportGenerateWithNoStoredReportStampsNow proves an empty store
// still yields a digest timestamped now, never the year-1 zero time a
// missing report would otherwise carry into the rendered header.
func TestReportGenerateWithNoStoredReportStampsNow(t *testing.T) {
	deps, fk := newTestDeps()
	fk.Store.LatestFound = false // no report saved yet

	before := time.Now()
	out := runCLI(t, deps, "--json", "report", "generate")
	after := time.Now()

	var got struct {
		GeneratedAt time.Time `json:"generatedAt"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if got.GeneratedAt.Before(before) || got.GeneratedAt.After(after) {
		t.Errorf("generatedAt = %s, want a stamp between %s and %s", got.GeneratedAt, before, after)
	}
}

// TestReportGenerateNoStoredReportRendersRealDate guards the rendered
// text, which is what actually lands in the operator's inbox.
func TestReportGenerateNoStoredReportRendersRealDate(t *testing.T) {
	deps, fk := newTestDeps()
	fk.Store.LatestFound = false

	out := runCLI(t, deps, "report", "generate")
	if strings.Contains(out, "0001-01-01") {
		t.Errorf("digest rendered the zero time: %q", out)
	}
	if !strings.Contains(out, time.Now().Format("2006-01-02")) {
		t.Errorf("digest should carry today's date, got %q", out)
	}
}

func TestReportGenerateRendersStoredFindings(t *testing.T) {
	deps, fk := newTestDeps()
	at := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	fk.Store.Findings = []store.StoredFinding{
		{
			Finding: check.Finding{
				CheckID:   "arr_health",
				Node:      config.NodePi,
				EntityKey: "sonarr",
				Severity:  check.SeverityCritical,
				Tier:      check.TierObserve,
				Summary:   "sonarr unreachable: canned finding",
				FirstSeen: at,
				LastSeen:  at,
			},
			Status: "open",
		},
	}
	fk.Store.LatestRep = check.Report{Node: config.NodePi, GeneratedAt: at, ChecksRun: 3}
	fk.Store.LatestFound = true

	out := runCLI(t, deps, "report", "generate")
	if fk.Store.Opened == false {
		t.Fatal("expected report generate (no --dry-run) to open the store")
	}
	if !strings.Contains(out, "sonarr unreachable: canned finding") {
		t.Errorf("expected the canned finding's summary in the digest, got %q", out)
	}
}
