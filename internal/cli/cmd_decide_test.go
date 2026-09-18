package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/decision"
)

// newDecideTestDeps wires newTestDeps' fakes (Sonarr/Radarr/Overseerr/
// Docker/Host/...) onto a config pointing at a real, temporary sqlite
// file. decision.Execute needs the concrete *store.Store (DecisionStore's
// compile-time assertion is against *store.Store, not the narrower
// StoreAPI/AgentStoreCloser Deps hooks), so cmd_decide.go opens the real
// store directly via store.Open rather than through
// deps.OpenStore/OpenAgentStore — mirroring how internal/store's own
// tests exercise a real store against a temp file rather than a fake.
func newDecideTestDeps(t *testing.T) (*Deps, *fakes, string) {
	t.Helper()
	deps, fk := newTestDeps()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{
			Node:      config.NodePi,
			State:     config.State{DBPath: dbPath},
			Staleness: config.Staleness{SnoozeDays: 60},
		}, config.Secrets{}, nil
	}
	return deps, fk, dbPath
}

// runDecide builds newDecideCmd(deps, flags) directly (per the task
// brief: it is not wired into NewRootCmd, so tests exercise it standalone
// rather than through the full root cobra tree) and executes args
// against it.
func runDecideCmd(t *testing.T, deps *Deps, flags *GlobalFlags, args ...string) (string, error) {
	t.Helper()
	cmd := newDecideCmd(deps, flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestDecideKeepDryRunNeverOpensStore(t *testing.T) {
	deps, _, dbPath := newDecideTestDeps(t)
	out, err := runDecideCmd(t, deps, &GlobalFlags{DryRun: true}, "keep", "sonarr:1")
	if err != nil {
		t.Fatalf("runDecide: %v", err)
	}
	if !strings.Contains(out, "would decide keep sonarr:1") {
		t.Errorf("output = %q, want a dry-run message", out)
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		t.Error("--dry-run must not create the store file")
	}
}

func TestDecideDeleteDryRunNeverOpensStore(t *testing.T) {
	deps, _, dbPath := newDecideTestDeps(t)
	out, err := runDecideCmd(t, deps, &GlobalFlags{DryRun: true}, "delete", "sonarr:1")
	if err != nil {
		t.Fatalf("runDecide: %v", err)
	}
	if !strings.Contains(out, "would decide delete sonarr:1") {
		t.Errorf("output = %q, want a dry-run message", out)
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		t.Error("--dry-run must not create the store file")
	}
}

func TestDecideKeepCreatesAndExecutesDecision(t *testing.T) {
	deps, _, _ := newDecideTestDeps(t)
	out, err := runDecideCmd(t, deps, &GlobalFlags{JSON: true}, "keep", "sonarr:1")
	if err != nil {
		t.Fatalf("runDecide: %v", err)
	}

	var got struct {
		ID          int64
		EntityKey   string
		Status      string
		SnoozeUntil time.Time
	}
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("invalid JSON %q: %v", out, jsonErr)
	}
	if got.ID == 0 || got.EntityKey != "sonarr:1" || got.Status != "executed" {
		t.Fatalf("got = %+v, want a real id, entityKey sonarr:1, status executed", got)
	}
	wantUntil := time.Now().AddDate(0, 0, 60)
	if got.SnoozeUntil.Before(wantUntil.Add(-time.Minute)) || got.SnoozeUntil.After(wantUntil.Add(time.Minute)) {
		t.Errorf("SnoozeUntil = %v, want close to %v (config's 60-day default)", got.SnoozeUntil, wantUntil)
	}
}

func TestDecideKeepSnoozeFlagOverridesConfig(t *testing.T) {
	deps, _, _ := newDecideTestDeps(t)
	out, err := runDecideCmd(t, deps, &GlobalFlags{JSON: true}, "keep", "sonarr:1", "--snooze", "5")
	if err != nil {
		t.Fatalf("runDecide: %v", err)
	}

	var got struct{ SnoozeUntil time.Time }
	if jsonErr := json.Unmarshal([]byte(out), &got); jsonErr != nil {
		t.Fatalf("invalid JSON %q: %v", out, jsonErr)
	}
	wantUntil := time.Now().AddDate(0, 0, 5)
	if got.SnoozeUntil.Before(wantUntil.Add(-time.Minute)) || got.SnoozeUntil.After(wantUntil.Add(time.Minute)) {
		t.Errorf("SnoozeUntil = %v, want close to %v (--snooze 5)", got.SnoozeUntil, wantUntil)
	}
}

func TestDecideDeleteBlockedWhenActionsDisabled(t *testing.T) {
	deps, fk, _ := newDecideTestDeps(t)
	_, err := runDecideCmd(t, deps, &GlobalFlags{}, "delete", "sonarr:1")
	if !errors.Is(err, decision.ErrActionsDisabled) {
		t.Fatalf("runDecide err = %v, want wrapping decision.ErrActionsDisabled", err)
	}
	if len(fk.Sonarr.Calls) != 0 {
		t.Errorf("sonarr Calls = %v, want none (gate checked before any client write)", fk.Sonarr.Calls)
	}
}

func TestDecideDeleteExecutesWhenActionsEnabled(t *testing.T) {
	deps, fk, dbPath := newDecideTestDeps(t)
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{
			Node:     config.NodePi,
			State:    config.State{DBPath: dbPath},
			Actions:  config.Actions{Enabled: true},
			Services: config.Services{Sonarr: config.Service{URL: "http://sonarr:8989"}},
		}, config.Secrets{}, nil
	}
	fk.Sonarr.SeriesList = []sonarr.Series{{ID: 1, Title: "Show"}}

	out, err := runDecideCmd(t, deps, &GlobalFlags{JSON: true}, "delete", "sonarr:1")
	if err != nil {
		t.Fatalf("runDecide: %v", err)
	}
	if len(fk.Sonarr.Calls) == 0 {
		t.Fatal("sonarr Calls = none, want DeleteSeries")
	}
	var found bool
	for _, c := range fk.Sonarr.Calls {
		if c == "DeleteSeries(1,true,true)" {
			found = true
		}
	}
	if !found {
		t.Errorf("sonarr Calls = %v, want DeleteSeries(1,true,true)", fk.Sonarr.Calls)
	}
	if !strings.Contains(out, `"Status": "executed"`) {
		t.Errorf("output = %q, want status executed", out)
	}
}

func TestDecideRequiresExactlyOneEntityKeyArg(t *testing.T) {
	deps, _, _ := newDecideTestDeps(t)
	if _, err := runDecideCmd(t, deps, &GlobalFlags{}, "keep"); err == nil {
		t.Fatal("expected an error with no entity-key argument")
	}
	if _, err := runDecideCmd(t, deps, &GlobalFlags{}, "keep", "a", "b"); err == nil {
		t.Fatal("expected an error with two entity-key arguments")
	}
}

func TestDecideStoreOpenFailurePropagates(t *testing.T) {
	deps, _, _ := newDecideTestDeps(t)
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodePi, State: config.State{DBPath: "/no/such/dir/state.db"}}, config.Secrets{}, nil
	}
	if _, err := runDecideCmd(t, deps, &GlobalFlags{}, "keep", "sonarr:1"); err == nil {
		t.Fatal("expected an error opening the store at a nonexistent directory")
	}
}

func TestDecideLoadErrorPropagates(t *testing.T) {
	deps, _, _ := newDecideTestDeps(t)
	boom := errors.New("load boom")
	deps.Load = func(string) (config.Config, config.Secrets, error) { return config.Config{}, config.Secrets{}, boom }
	if _, err := runDecideCmd(t, deps, &GlobalFlags{}, "keep", "sonarr:1"); !errors.Is(err, boom) {
		t.Fatalf("runDecide err = %v, want wrapping %v", err, boom)
	}
}
