package web

import (
	"context"
	"errors"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// errBoom is a shared sentinel test fixtures use to simulate a store
// failure without caring about the exact message.
var errBoom = errors.New("boom: simulated store failure")

// Compile-time proof that *store.Store actually satisfies the Store
// interface this package reads through, so production wiring never
// needs an adapter for it (mirroring internal/decision's own
// DecisionStore assertion pattern).
var _ Store = (*store.Store)(nil)

// createDecisionCall records one CreateDecision invocation fakeStore
// captured, for tests to assert on.
type createDecisionCall struct {
	EntityKey string
	Kind      string
	At        time.Time
}

// fakeStore is an in-memory Store for tests: it returns caller-seeded
// data and records every write, mirroring how *store.Store would behave
// without a real database.
type fakeStore struct {
	OpenFindingsByNode map[config.Node][]store.StoredFinding
	OpenFindingsErr    error

	LatestReportByNode map[config.Node]check.Report
	LatestReportOK     map[config.Node]bool
	LatestReportErr    error

	Pending    []store.Decision
	PendingErr error

	CreateDecisionID    int64
	CreateDecisionErr   error
	CreateDecisionCalls []createDecisionCall

	Remediations    []store.Remediation
	RemediationsErr error

	HistoryByNode map[config.Node][]store.StoredFinding
	HistoryErr    error

	HeartbeatByNode map[config.Node]time.Time
	HeartbeatOK     map[config.Node]bool
	HeartbeatErr    error
}

var _ Store = (*fakeStore)(nil)

func (f *fakeStore) OpenFindings(_ context.Context, node config.Node) ([]store.StoredFinding, error) {
	if f.OpenFindingsErr != nil {
		return nil, f.OpenFindingsErr
	}
	return f.OpenFindingsByNode[node], nil
}

func (f *fakeStore) LatestReport(_ context.Context, node config.Node) (check.Report, bool, error) {
	if f.LatestReportErr != nil {
		return check.Report{}, false, f.LatestReportErr
	}
	return f.LatestReportByNode[node], f.LatestReportOK[node], nil
}

func (f *fakeStore) PendingDecisions(_ context.Context) ([]store.Decision, error) {
	if f.PendingErr != nil {
		return nil, f.PendingErr
	}
	return f.Pending, nil
}

func (f *fakeStore) CreateDecision(_ context.Context, entityKey, kind string, at time.Time) (int64, error) {
	f.CreateDecisionCalls = append(f.CreateDecisionCalls, createDecisionCall{EntityKey: entityKey, Kind: kind, At: at})
	if f.CreateDecisionErr != nil {
		return 0, f.CreateDecisionErr
	}
	return f.CreateDecisionID, nil
}

func (f *fakeStore) RecentRemediations(_ context.Context, _ time.Time) ([]store.Remediation, error) {
	if f.RemediationsErr != nil {
		return nil, f.RemediationsErr
	}
	return f.Remediations, nil
}

func (f *fakeStore) FindingHistory(_ context.Context, node config.Node, _ time.Time, _ int) ([]store.StoredFinding, error) {
	if f.HistoryErr != nil {
		return nil, f.HistoryErr
	}
	return f.HistoryByNode[node], nil
}

func (f *fakeStore) LastPeerMessageAt(_ context.Context, node config.Node, _ string) (time.Time, bool, error) {
	if f.HeartbeatErr != nil {
		return time.Time{}, false, f.HeartbeatErr
	}
	return f.HeartbeatByNode[node], f.HeartbeatOK[node], nil
}

// executeCall records one DecisionRunner.Execute invocation fakeRunner
// captured.
type executeCall struct {
	ID int64
}

// fakeRunner is a DecisionRunner test double.
type fakeRunner struct {
	Result store.Decision
	Err    error
	Calls  []executeCall
}

var _ DecisionRunner = (*fakeRunner)(nil)

func (f *fakeRunner) Execute(_ context.Context, id int64) (store.Decision, error) {
	f.Calls = append(f.Calls, executeCall{ID: id})
	return f.Result, f.Err
}
