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
	// notify.DigestInput carries no json tags, so keys are its Go field
	// names verbatim.
	if got["Node"] != "pi" {
		t.Errorf("Node = %v, want pi", got["Node"])
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
