package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/cleanup"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// cleanupFakeStore is a minimal StoreAPI + remediationStore fake, kept
// local to this file rather than extending the shared FakeStore
// (cmd_check_test.go) with a method it doesn't otherwise need.
type cleanupFakeStore struct {
	Opened    bool
	Closed    bool
	OpenErr   error
	CloseErr  error
	RecordErr error
	Recorded  []store.Remediation
}

var _ StoreAPI = (*cleanupFakeStore)(nil)
var _ remediationStore = (*cleanupFakeStore)(nil)

func (f *cleanupFakeStore) SaveReport(context.Context, check.Report) (int64, store.UpsertSummary, error) {
	return 0, store.UpsertSummary{}, nil
}
func (f *cleanupFakeStore) LatestReport(context.Context, config.Node) (check.Report, bool, error) {
	return check.Report{}, false, nil
}
func (f *cleanupFakeStore) OpenFindings(context.Context, config.Node) ([]store.StoredFinding, error) {
	return nil, nil
}
func (f *cleanupFakeStore) Close() error {
	f.Closed = true
	return f.CloseErr
}
func (f *cleanupFakeStore) RecordRemediation(_ context.Context, r store.Remediation) (int64, error) {
	if f.RecordErr != nil {
		return 0, f.RecordErr
	}
	f.Recorded = append(f.Recorded, r)
	return int64(len(f.Recorded)), nil
}

// newCleanupTestDeps builds a *Deps wired with a cleanupFakeStore (via
// OpenStore) plus the fakes newTestDeps already provides, so cleanup
// tests can assert on both the recorded remediation and the underlying
// client calls (hostfs/docker/qbittorrent).
func newCleanupTestDeps() (*Deps, *fakes, *cleanupFakeStore) {
	deps, fk := newTestDeps()
	cs := &cleanupFakeStore{}
	deps.OpenStore = func(context.Context, config.Config) (StoreAPI, error) {
		cs.Opened = true
		return cs, nil
	}
	return deps, fk, cs
}

// runCleanupCmd builds `cleanup <kind>` standalone (per the brief: no
// root registration yet) and executes it directly against flags, letting
// a test set flags.DryRun/flags.JSON before calling rather than parsing
// them from args.
func runCleanupCmd(t *testing.T, deps *Deps, flags *GlobalFlags, kind string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := newCleanupCmd(deps, flags)
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{kind})
	err := cmd.Execute()
	return buf.String(), err
}

func nasRecycleLoad(over func(*config.Config)) func(string) (config.Config, config.Secrets, error) {
	return func(string) (config.Config, config.Secrets, error) {
		cfg := config.Config{
			Node:    config.NodeNAS,
			Checks:  config.Checks{RecycleDirs: []string{"/recycle"}},
			Cleanup: config.Cleanup{RecycleMinAge: 24 * time.Hour},
		}
		if over != nil {
			over(&cfg)
		}
		return cfg, config.Secrets{}, nil
	}
}

func TestCleanupGlobalDryRunNeverOpensStore(t *testing.T) {
	deps, fk, cs := newCleanupTestDeps()
	deps.Load = nasRecycleLoad(nil)
	fk.Host.Entries = map[string][]hostfs.Entry{
		"/recycle": {{Name: "old.mkv", Size: 42, ModTime: time.Now().Add(-48 * time.Hour)}},
	}
	flags := &GlobalFlags{DryRun: true}

	out, err := runCleanupCmd(t, deps, flags, "recycle")
	if err != nil {
		t.Fatalf("execute: %v (out=%s)", err, out)
	}
	if cs.Opened {
		t.Error("expected --dry-run to never open the store")
	}
	if !strings.Contains(out, "status: planned") {
		t.Errorf("expected planned status in output, got %q", out)
	}
	if len(fk.Host.Removed) != 0 {
		t.Errorf("expected nothing removed under --dry-run, got %v", fk.Host.Removed)
	}
}

func TestCleanupBlockedWhenActionsDisabled(t *testing.T) {
	deps, fk, cs := newCleanupTestDeps()
	deps.Load = nasRecycleLoad(func(c *config.Config) { c.Actions.Enabled = false })
	fk.Host.Entries = map[string][]hostfs.Entry{
		"/recycle": {{Name: "old.mkv", Size: 42, ModTime: time.Now().Add(-48 * time.Hour)}},
	}
	flags := &GlobalFlags{}

	out, err := runCleanupCmd(t, deps, flags, "recycle")
	if err != nil {
		t.Fatalf("execute: %v (out=%s)", err, out)
	}
	if !cs.Opened || len(cs.Recorded) != 1 {
		t.Fatalf("expected exactly one remediation recorded, got %d (opened=%v)", len(cs.Recorded), cs.Opened)
	}
	rec := cs.Recorded[0]
	if rec.Status != "blocked" || !rec.DryRun || rec.Action != "cleanup:recycle" || rec.Tier != "correct" {
		t.Errorf("recorded remediation = %+v, want status=blocked dryRun=true action=cleanup:recycle tier=correct", rec)
	}
	if len(fk.Host.Removed) != 0 {
		t.Errorf("expected nothing removed when actions are disabled, got %v", fk.Host.Removed)
	}
}

func TestCleanupPlannedWhenCleanupConfigDryRunButActionsEnabled(t *testing.T) {
	deps, fk, cs := newCleanupTestDeps()
	deps.Load = nasRecycleLoad(func(c *config.Config) {
		c.Actions.Enabled = true
		c.Cleanup.DryRun = true
	})
	fk.Host.Entries = map[string][]hostfs.Entry{
		"/recycle": {{Name: "old.mkv", Size: 42, ModTime: time.Now().Add(-48 * time.Hour)}},
	}
	flags := &GlobalFlags{}

	if _, err := runCleanupCmd(t, deps, flags, "recycle"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(cs.Recorded) != 1 || cs.Recorded[0].Status != "planned" || !cs.Recorded[0].DryRun {
		t.Fatalf("recorded = %+v, want one row with status=planned dryRun=true", cs.Recorded)
	}
	if len(fk.Host.Removed) != 0 {
		t.Errorf("expected nothing removed while config.Cleanup.DryRun is true, got %v", fk.Host.Removed)
	}
}

func TestCleanupExecutesWhenConfigAllows(t *testing.T) {
	deps, fk, cs := newCleanupTestDeps()
	deps.Load = nasRecycleLoad(func(c *config.Config) { c.Actions.Enabled = true })
	old := time.Now().Add(-48 * time.Hour)
	fk.Host.Entries = map[string][]hostfs.Entry{"/recycle": {{Name: "old.mkv", Size: 42, ModTime: old}}}
	flags := &GlobalFlags{}

	out, err := runCleanupCmd(t, deps, flags, "recycle")
	if err != nil {
		t.Fatalf("execute: %v (out=%s)", err, out)
	}
	if len(fk.Host.Removed) != 1 || fk.Host.Removed[0] != "/recycle/old.mkv" {
		t.Fatalf("Removed = %v, want [/recycle/old.mkv]", fk.Host.Removed)
	}
	if len(cs.Recorded) != 1 || cs.Recorded[0].Status != "executed" || cs.Recorded[0].DryRun {
		t.Fatalf("recorded = %+v, want one row with status=executed dryRun=false", cs.Recorded)
	}
	if !strings.Contains(out, "result: 1 executed") {
		t.Errorf("expected a result summary line, got %q", out)
	}
}

func TestCleanupJSONOutputShape(t *testing.T) {
	deps, fk, _ := newCleanupTestDeps()
	deps.Load = nasRecycleLoad(func(c *config.Config) { c.Actions.Enabled = true })
	old := time.Now().Add(-48 * time.Hour)
	fk.Host.Entries = map[string][]hostfs.Entry{"/recycle": {{Name: "old.mkv", Size: 42, ModTime: old}}}
	flags := &GlobalFlags{JSON: true}

	out, err := runCleanupCmd(t, deps, flags, "recycle")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got cleanupOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if got.Kind != "recycle" || got.Status != "executed" || got.DryRun {
		t.Errorf("decoded = %+v", got)
	}
	if got.Result == nil || got.Result.Executed != 1 {
		t.Errorf("Result = %+v, want Executed=1", got.Result)
	}
}

func TestCleanupPlanErrorRecordsFailedStatus(t *testing.T) {
	deps, _, cs := newCleanupTestDeps()
	// No RecycleDirs configured: the planner returns check.ErrNotConfigured.
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodeNAS}, config.Secrets{}, nil
	}
	flags := &GlobalFlags{}

	_, err := runCleanupCmd(t, deps, flags, "recycle")
	if err == nil {
		t.Fatal("expected the planner's ErrNotConfigured to propagate")
	}
	if len(cs.Recorded) != 1 || cs.Recorded[0].Status != "failed" {
		t.Fatalf("recorded = %+v, want one row with status=failed", cs.Recorded)
	}
}

func TestCleanupPlanErrorUnderDryRunNeverOpensStore(t *testing.T) {
	deps, _, cs := newCleanupTestDeps()
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodeNAS}, config.Secrets{}, nil
	}
	flags := &GlobalFlags{DryRun: true}

	if _, err := runCleanupCmd(t, deps, flags, "recycle"); err == nil {
		t.Fatal("expected the planner's ErrNotConfigured to propagate even under --dry-run")
	}
	if cs.Opened {
		t.Error("expected --dry-run to never open the store even when planning fails")
	}
}

func TestCleanupNodeMismatchErrors(t *testing.T) {
	deps, _, cs := newCleanupTestDeps()
	// docker is Pi-only; this config is on the NAS node.
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodeNAS}, config.Secrets{}, nil
	}
	flags := &GlobalFlags{}

	if _, err := runCleanupCmd(t, deps, flags, "docker"); err == nil {
		t.Fatal("expected an error: docker does not run on the nas node")
	}
	if cs.Opened {
		t.Error("expected a node mismatch to never open the store")
	}
}

func TestCleanupInvalidKindRejectedByCobra(t *testing.T) {
	deps, _, _ := newCleanupTestDeps()
	flags := &GlobalFlags{}
	if _, err := runCleanupCmd(t, deps, flags, "not-a-kind"); err == nil {
		t.Fatal("expected an error for an invalid kind")
	}
}

func TestCleanupBuildCheckDepsFailurePropagates(t *testing.T) {
	deps, _, cs := newCleanupTestDeps()
	boom := errors.New("boom")
	deps.Host = func(config.Config, config.Secrets) (hostfs.Client, error) { return nil, boom }
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodeNAS}, config.Secrets{}, nil
	}
	flags := &GlobalFlags{}

	if _, err := runCleanupCmd(t, deps, flags, "recycle"); err == nil {
		t.Fatal("expected the host client constructor's error to propagate")
	}
	if cs.Opened {
		t.Error("expected a check-deps build failure to never open the store")
	}
}

func TestCleanupStoreOpenFailurePropagates(t *testing.T) {
	deps, fk, _ := newCleanupTestDeps()
	deps.Load = nasRecycleLoad(func(c *config.Config) { c.Actions.Enabled = true })
	fk.Host.Entries = map[string][]hostfs.Entry{
		"/recycle": {{Name: "old.mkv", Size: 42, ModTime: time.Now().Add(-48 * time.Hour)}},
	}
	boom := errors.New("store down")
	deps.OpenStore = func(context.Context, config.Config) (StoreAPI, error) { return nil, boom }
	flags := &GlobalFlags{}

	if _, err := runCleanupCmd(t, deps, flags, "recycle"); err == nil {
		t.Fatal("expected the store-open failure to propagate")
	}
}

// storeAPIOnly implements StoreAPI but deliberately not RecordRemediation,
// covering recordCleanup's defensive type-assertion failure: a StoreAPI
// implementation that can't record remediations must surface a clear
// error rather than a nil-method panic.
type storeAPIOnly struct{}

var _ StoreAPI = storeAPIOnly{}

func (storeAPIOnly) SaveReport(context.Context, check.Report) (int64, store.UpsertSummary, error) {
	return 0, store.UpsertSummary{}, nil
}
func (storeAPIOnly) LatestReport(context.Context, config.Node) (check.Report, bool, error) {
	return check.Report{}, false, nil
}
func (storeAPIOnly) OpenFindings(context.Context, config.Node) ([]store.StoredFinding, error) {
	return nil, nil
}
func (storeAPIOnly) Close() error { return nil }

func TestCleanupRecordUnsupportedStoreErrors(t *testing.T) {
	deps, fk := newTestDeps()
	deps.OpenStore = func(context.Context, config.Config) (StoreAPI, error) { return storeAPIOnly{}, nil }
	deps.Load = nasRecycleLoad(func(c *config.Config) { c.Actions.Enabled = false })
	fk.Host.Entries = map[string][]hostfs.Entry{
		"/recycle": {{Name: "old.mkv", Size: 42, ModTime: time.Now().Add(-48 * time.Hour)}},
	}
	flags := &GlobalFlags{}

	if _, err := runCleanupCmd(t, deps, flags, "recycle"); err == nil {
		t.Fatal("expected an error: storeAPIOnly does not implement remediationStore")
	}
}

func TestCleanupCloseErrorIsJoinedNotSwallowed(t *testing.T) {
	deps, fk, cs := newCleanupTestDeps()
	deps.Load = nasRecycleLoad(func(c *config.Config) { c.Actions.Enabled = false })
	fk.Host.Entries = map[string][]hostfs.Entry{
		"/recycle": {{Name: "old.mkv", Size: 42, ModTime: time.Now().Add(-48 * time.Hour)}},
	}
	cs.CloseErr = errors.New("close boom")
	flags := &GlobalFlags{}

	_, err := runCleanupCmd(t, deps, flags, "recycle")
	if err == nil || !strings.Contains(err.Error(), "close boom") {
		t.Fatalf("expected the close error to surface, got %v", err)
	}
}

func TestPlanAndResultSummaryFormatting(t *testing.T) {
	// Direct unit coverage of the Detail-column formatting helpers,
	// independent of the full CLI plumbing above.
	ps := planSummary(cleanup.Plan{Items: []cleanup.PlanItem{{Key: "a", Bytes: 42}}, Bytes: 42})
	if !strings.Contains(ps, "1 item(s) planned") || !strings.Contains(ps, "42 byte(s)") {
		t.Errorf("planSummary = %q", ps)
	}
	rs := resultSummary(cleanup.Result{Executed: 1, Bytes: 42, Errors: []string{"one boom"}})
	if !strings.Contains(rs, "1 item(s) executed") || !strings.Contains(rs, "one boom") {
		t.Errorf("resultSummary = %q", rs)
	}
}
