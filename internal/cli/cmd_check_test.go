package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/checks"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// FakeStore is an in-memory StoreAPI for tests: it records every SaveReport
// call and returns canned data for LatestReport/OpenFindings without
// touching disk. Opened is set by the test's OpenStore closure (never by
// FakeStore itself) so a test can assert the store was never opened.
type FakeStore struct {
	Opened bool
	Closed bool

	Saved        []check.Report
	SaveErr      error
	UpsertResult store.UpsertSummary

	LatestRep   check.Report
	LatestFound bool
	LatestErr   error

	Findings    []store.StoredFinding
	FindingsErr error

	CloseErr error
}

var _ StoreAPI = (*FakeStore)(nil)

func (f *FakeStore) SaveReport(_ context.Context, rep check.Report) (int64, store.UpsertSummary, error) {
	if f.SaveErr != nil {
		return 0, store.UpsertSummary{}, f.SaveErr
	}
	f.Saved = append(f.Saved, rep)
	return int64(len(f.Saved)), f.UpsertResult, nil
}

func (f *FakeStore) LatestReport(context.Context, config.Node) (check.Report, bool, error) {
	return f.LatestRep, f.LatestFound, f.LatestErr
}

func (f *FakeStore) OpenFindings(context.Context, config.Node) ([]store.StoredFinding, error) {
	return f.Findings, f.FindingsErr
}

func (f *FakeStore) Close() error {
	f.Closed = true
	return f.CloseErr
}

func TestCheckListShowsEveryRegisteredCheck(t *testing.T) {
	deps, _ := newTestDeps()
	out := runCLI(t, deps, "check", "list")

	reg, err := checks.Registry(config.Config{})
	if err != nil {
		t.Fatalf("checks.Registry: %v", err)
	}
	want := len(reg.All())

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	got := len(lines) - 1 // minus header row
	if got != want {
		t.Fatalf("check list printed %d rows, want %d (all registered checks): output=%q", got, want, out)
	}
}

func TestCheckListJSON(t *testing.T) {
	deps, _ := newTestDeps()
	out := runCLI(t, deps, "--json", "check", "list")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}
}

func TestCheckRunAllDryRunNeverOpensStoreAndReportsArrHealth(t *testing.T) {
	deps, fk := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{
			Node:     config.NodePi,
			Services: config.Services{Sonarr: config.Service{URL: "http://sonarr:8989"}},
			Checks:   config.Checks{Timeout: testCheckTimeout},
		}, config.Secrets{}, nil
	}
	fk.Sonarr.HealthItems = []sonarr.HealthItem{{Type: "error", Source: "X", Message: "boom"}}

	out := runCLI(t, deps, "--dry-run", "check", "run", "--all")

	if fk.Store.Opened {
		t.Error("expected --dry-run to never open the store")
	}
	if !strings.Contains(out, "arr_health") {
		t.Errorf("expected an arr_health row, got %q", out)
	}
	if !strings.Contains(out, "critical") {
		t.Errorf("expected a critical severity row, got %q", out)
	}
}

func TestCheckRunAllPersistsAndReportsInJSON(t *testing.T) {
	deps, fk := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodePi, Checks: config.Checks{Timeout: testCheckTimeout}}, config.Secrets{}, nil
	}

	out := runCLI(t, deps, "--json", "check", "run", "--all")

	if len(fk.Store.Saved) != 1 {
		t.Fatalf("expected SaveReport called once, got %d", len(fk.Store.Saved))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if persisted, ok := got["persisted"].(bool); !ok || !persisted {
		t.Errorf("expected persisted=true in JSON, got %v", got["persisted"])
	}
}

func TestCheckRunIDNotApplicableToNodeErrors(t *testing.T) {
	deps, _ := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodeNAS, Checks: config.Checks{Timeout: testCheckTimeout}}, config.Secrets{}, nil
	}
	if _, err := runCLIErr(t, deps, "check", "run", "--id", "mount_race"); err == nil {
		t.Fatal("expected an error: mount_race is pi-only, node is nas")
	}
}

func TestCheckRunRequiresExactlyOneOfAllOrID(t *testing.T) {
	deps, _ := newTestDeps()
	if _, err := runCLIErr(t, deps, "check", "run"); err == nil {
		t.Fatal("expected an error when neither --all nor --id is given")
	}
	if _, err := runCLIErr(t, deps, "check", "run", "--all", "--id", "mount_race"); err == nil {
		t.Fatal("expected an error when both --all and --id are given")
	}
}

func TestCheckRunUnknownIDErrors(t *testing.T) {
	deps, _ := newTestDeps()
	if _, err := runCLIErr(t, deps, "check", "run", "--id", "no_such_check"); err == nil {
		t.Fatal("expected an error for an unknown check id")
	}
}

func TestCheckRunStoreOpenFailurePropagates(t *testing.T) {
	deps, _ := newTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodePi, Checks: config.Checks{Timeout: testCheckTimeout}}, config.Secrets{}, nil
	}
	boom := errors.New("boom")
	deps.OpenStore = func(context.Context, config.Config) (StoreAPI, error) { return nil, boom }
	if _, err := runCLIErr(t, deps, "check", "run", "--all"); err == nil {
		t.Fatal("expected store open failure to propagate")
	}
}
