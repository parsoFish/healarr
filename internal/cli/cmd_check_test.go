package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/agent"
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/checks"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/decision"
	"github.com/parsoFish/healarr/internal/store"
	"github.com/parsoFish/healarr/internal/web"
)

// storePeerMsgCall is one SavePeerMessage invocation FakeStore recorded.
type storePeerMsgCall struct {
	Direction, Kind string
	Peer            config.Node
	Payload         []byte
	At              time.Time
}

// storeEnqueuedEmail is one EnqueueEmail invocation FakeStore recorded.
type storeEnqueuedEmail struct {
	To, Subject, Body string
	At                time.Time
}

// storeFailedEmailCall is one MarkEmailFailed invocation FakeStore recorded.
type storeFailedEmailCall struct {
	ID    int64
	At    time.Time
	Cause error
}

// FakeStore is an in-memory StoreAPI (and, since Phase 3, AgentStoreCloser)
// for tests: it records every call and returns canned data without
// touching disk, mirroring how *store.Store satisfies both interfaces.
// Opened is set by the test's OpenStore/OpenAgentStore closure (never by
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

	// Phase 3 (agent.Store) additions.
	PeerMessages       []storePeerMsgCall
	SavePeerMessageErr error
	nextMsgID          int64

	LastPeerAt    time.Time
	LastPeerFound bool
	LastPeerErr   error

	Emails      []storeEnqueuedEmail
	EnqueueErr  error
	nextEmailID int64

	SentEmailIDs []int64
	MarkSentErr  error

	FailedEmails  []storeFailedEmailCall
	MarkFailedErr error

	CheckpointCalls int
	CheckpointErr   error

	PrunedBefore     []time.Time
	PrunedDeleted    int64
	PrunePeerMsgsErr error

	// Phase 4 (decision/qbit_delete) addition.
	Remediations         []store.Remediation
	RecordRemediationErr error
	nextRemediationID    int64

	// Phase 4 Task 7 (web decisions wiring for `agent serve` on the pi)
	// additions: FakeStore also needs to satisfy web.Store and
	// decision.DecisionStore so `agent serve`'s runtime type assertions
	// (see cmd_agent.go's buildWebHandler) succeed against it in tests.
	// Their canned behaviour is exercised by internal/web's and
	// internal/decision's own tests; here they only need to compile and
	// return harmless, configurable defaults.
	CreateDecisionID    int64
	CreateDecisionErr   error
	CreateDecisionCalls []storeCreateDecisionCall

	PendingDecisionsResult []store.Decision
	PendingDecisionsErr    error

	DecisionsByID map[int64]store.Decision

	MarkDecisionErr   error
	MarkDecisionCalls []storeMarkDecisionCall

	SnoozeEntityErr   error
	SnoozeEntityCalls []storeSnoozeEntityCall

	RecentRemediationsResult []store.Remediation
	RecentRemediationsErr    error

	FindingHistoryResult []store.StoredFinding
	FindingHistoryErr    error
}

var _ StoreAPI = (*FakeStore)(nil)
var _ agent.Store = (*FakeStore)(nil)
var _ AgentStoreCloser = (*FakeStore)(nil)
var _ web.Store = (*FakeStore)(nil)
var _ decision.DecisionStore = (*FakeStore)(nil)

// storeCreateDecisionCall is one CreateDecision invocation FakeStore
// recorded.
type storeCreateDecisionCall struct {
	EntityKey, Kind string
	At              time.Time
}

// storeMarkDecisionCall is one MarkDecision invocation FakeStore recorded.
type storeMarkDecisionCall struct {
	ID     int64
	Status string
	At     time.Time
	Cause  error
}

// storeSnoozeEntityCall is one SnoozeEntity invocation FakeStore recorded.
type storeSnoozeEntityCall struct {
	EntityKey string
	Until     time.Time
}

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

// SavePeerMessage records the call and returns an auto-incrementing id
// (starting at 1, like a real sqlite rowid) unless SavePeerMessageErr is
// set.
func (f *FakeStore) SavePeerMessage(_ context.Context, direction, kind string, peer config.Node, payload []byte, at time.Time) (int64, error) {
	f.PeerMessages = append(f.PeerMessages, storePeerMsgCall{Direction: direction, Kind: kind, Peer: peer, Payload: payload, At: at})
	if f.SavePeerMessageErr != nil {
		return 0, f.SavePeerMessageErr
	}
	f.nextMsgID++
	return f.nextMsgID, nil
}

// LastPeerMessageAt returns the canned LastPeerAt/LastPeerFound/LastPeerErr.
func (f *FakeStore) LastPeerMessageAt(_ context.Context, _ config.Node, _ string) (time.Time, bool, error) {
	if f.LastPeerErr != nil {
		return time.Time{}, false, f.LastPeerErr
	}
	return f.LastPeerAt, f.LastPeerFound, nil
}

// EnqueueEmail records the call and returns an auto-incrementing id
// (starting at 1) unless EnqueueErr is set.
func (f *FakeStore) EnqueueEmail(_ context.Context, to, subject, body string, at time.Time) (int64, error) {
	if f.EnqueueErr != nil {
		return 0, f.EnqueueErr
	}
	f.nextEmailID++
	f.Emails = append(f.Emails, storeEnqueuedEmail{To: to, Subject: subject, Body: body, At: at})
	return f.nextEmailID, nil
}

// MarkEmailSent records id and returns MarkSentErr.
func (f *FakeStore) MarkEmailSent(_ context.Context, id int64, _ time.Time) error {
	f.SentEmailIDs = append(f.SentEmailIDs, id)
	return f.MarkSentErr
}

// MarkEmailFailed records the call and returns MarkFailedErr.
func (f *FakeStore) MarkEmailFailed(_ context.Context, id int64, at time.Time, cause error) error {
	f.FailedEmails = append(f.FailedEmails, storeFailedEmailCall{ID: id, At: at, Cause: cause})
	return f.MarkFailedErr
}

// Checkpoint counts the call and returns CheckpointErr.
func (f *FakeStore) PrunePeerMessages(_ context.Context, olderThan time.Time) (int64, error) {
	f.PrunedBefore = append(f.PrunedBefore, olderThan)
	if f.PrunePeerMsgsErr != nil {
		return 0, f.PrunePeerMsgsErr
	}
	return f.PrunedDeleted, nil
}

func (f *FakeStore) Checkpoint(context.Context) error {
	f.CheckpointCalls++
	return f.CheckpointErr
}

// RecordRemediation records r and returns an auto-incrementing id
// (starting at 1, like a real sqlite rowid) unless RecordRemediationErr
// is set.
func (f *FakeStore) RecordRemediation(_ context.Context, r store.Remediation) (int64, error) {
	f.Remediations = append(f.Remediations, r)
	if f.RecordRemediationErr != nil {
		return 0, f.RecordRemediationErr
	}
	f.nextRemediationID++
	return f.nextRemediationID, nil
}

// CreateDecision records the call and returns CreateDecisionID/Err.
func (f *FakeStore) CreateDecision(_ context.Context, entityKey, kind string, at time.Time) (int64, error) {
	f.CreateDecisionCalls = append(f.CreateDecisionCalls, storeCreateDecisionCall{EntityKey: entityKey, Kind: kind, At: at})
	if f.CreateDecisionErr != nil {
		return 0, f.CreateDecisionErr
	}
	return f.CreateDecisionID, nil
}

// PendingDecisions returns the canned PendingDecisionsResult/Err.
func (f *FakeStore) PendingDecisions(context.Context) ([]store.Decision, error) {
	if f.PendingDecisionsErr != nil {
		return nil, f.PendingDecisionsErr
	}
	return f.PendingDecisionsResult, nil
}

// DecisionByID looks id up in DecisionsByID.
func (f *FakeStore) DecisionByID(_ context.Context, id int64) (store.Decision, bool, error) {
	d, ok := f.DecisionsByID[id]
	return d, ok, nil
}

// MarkDecision records the call and returns MarkDecisionErr.
func (f *FakeStore) MarkDecision(_ context.Context, id int64, status string, at time.Time, cause error) error {
	f.MarkDecisionCalls = append(f.MarkDecisionCalls, storeMarkDecisionCall{ID: id, Status: status, At: at, Cause: cause})
	return f.MarkDecisionErr
}

// SnoozeEntity records the call and returns SnoozeEntityErr.
func (f *FakeStore) SnoozeEntity(_ context.Context, entityKey string, until time.Time) error {
	f.SnoozeEntityCalls = append(f.SnoozeEntityCalls, storeSnoozeEntityCall{EntityKey: entityKey, Until: until})
	return f.SnoozeEntityErr
}

// RecentRemediations returns the canned RecentRemediationsResult/Err.
func (f *FakeStore) RecentRemediations(context.Context, time.Time) ([]store.Remediation, error) {
	if f.RecentRemediationsErr != nil {
		return nil, f.RecentRemediationsErr
	}
	return f.RecentRemediationsResult, nil
}

// FindingHistory returns the canned FindingHistoryResult/Err.
func (f *FakeStore) FindingHistory(context.Context, config.Node, time.Time, int) ([]store.StoredFinding, error) {
	if f.FindingHistoryErr != nil {
		return nil, f.FindingHistoryErr
	}
	return f.FindingHistoryResult, nil
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
