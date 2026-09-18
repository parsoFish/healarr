package agent

import (
	"context"
	"sync"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
)

// peerMsgCall is one SavePeerMessage invocation fakeStore recorded.
type peerMsgCall struct {
	Direction, Kind string
	Peer            config.Node
	Payload         []byte
	At              time.Time
}

// failedEmailCall is one MarkEmailFailed invocation fakeStore recorded.
type failedEmailCall struct {
	ID    int64
	At    time.Time
	Cause error
}

// enqueuedEmail is one EnqueueEmail invocation fakeStore recorded.
type enqueuedEmail struct {
	To, Subject, Body string
	At                time.Time
}

// fakeStore is an in-memory agent.Store: every method records its call
// and returns a caller-configured result/error, mirroring internal/peer's
// Fake. Configure the *Err/*Result fields before use; nextReportID and
// nextMsgID/nextEmailID auto-increment from 1 like real sqlite rowids.
type fakeStore struct {
	SaveReportErr error
	SavedReports  []check.Report
	nextReportID  int64

	LatestReportResult check.Report
	LatestReportFound  bool
	LatestReportErr    error

	OpenFindingsResult []store.StoredFinding
	OpenFindingsErr    error

	SavePeerMessageErr error
	PeerMessages       []peerMsgCall
	nextMsgID          int64

	LastPeerMessageAtResult time.Time
	LastPeerMessageAtFound  bool
	LastPeerMessageAtErr    error

	EnqueueEmailErr error
	Emails          []enqueuedEmail
	nextEmailID     int64

	MarkEmailSentErr error
	SentEmailIDs     []int64

	MarkEmailFailedErr error
	FailedEmails       []failedEmailCall

	CheckpointErr   error
	CheckpointCalls int
}

var _ Store = (*fakeStore)(nil)

func (f *fakeStore) SaveReport(_ context.Context, rep check.Report) (int64, store.UpsertSummary, error) {
	if f.SaveReportErr != nil {
		return 0, store.UpsertSummary{}, f.SaveReportErr
	}
	f.nextReportID++
	f.SavedReports = append(f.SavedReports, rep)
	return f.nextReportID, store.UpsertSummary{}, nil
}

func (f *fakeStore) LatestReport(_ context.Context, _ config.Node) (check.Report, bool, error) {
	if f.LatestReportErr != nil {
		return check.Report{}, false, f.LatestReportErr
	}
	return f.LatestReportResult, f.LatestReportFound, nil
}

func (f *fakeStore) OpenFindings(_ context.Context, _ config.Node) ([]store.StoredFinding, error) {
	if f.OpenFindingsErr != nil {
		return nil, f.OpenFindingsErr
	}
	return f.OpenFindingsResult, nil
}

func (f *fakeStore) SavePeerMessage(_ context.Context, direction, kind string, peer config.Node, payload []byte, at time.Time) (int64, error) {
	f.PeerMessages = append(f.PeerMessages, peerMsgCall{Direction: direction, Kind: kind, Peer: peer, Payload: payload, At: at})
	if f.SavePeerMessageErr != nil {
		return 0, f.SavePeerMessageErr
	}
	f.nextMsgID++
	return f.nextMsgID, nil
}

func (f *fakeStore) LastPeerMessageAt(_ context.Context, _ config.Node, _ string) (time.Time, bool, error) {
	if f.LastPeerMessageAtErr != nil {
		return time.Time{}, false, f.LastPeerMessageAtErr
	}
	return f.LastPeerMessageAtResult, f.LastPeerMessageAtFound, nil
}

func (f *fakeStore) EnqueueEmail(_ context.Context, to, subject, body string, at time.Time) (int64, error) {
	if f.EnqueueEmailErr != nil {
		return 0, f.EnqueueEmailErr
	}
	f.nextEmailID++
	f.Emails = append(f.Emails, enqueuedEmail{To: to, Subject: subject, Body: body, At: at})
	return f.nextEmailID, nil
}

func (f *fakeStore) MarkEmailSent(_ context.Context, id int64, _ time.Time) error {
	f.SentEmailIDs = append(f.SentEmailIDs, id)
	return f.MarkEmailSentErr
}

func (f *fakeStore) MarkEmailFailed(_ context.Context, id int64, at time.Time, cause error) error {
	f.FailedEmails = append(f.FailedEmails, failedEmailCall{ID: id, At: at, Cause: cause})
	return f.MarkEmailFailedErr
}

func (f *fakeStore) Checkpoint(_ context.Context) error {
	f.CheckpointCalls++
	return f.CheckpointErr
}

// fakeDeps returns a Deps-building func for Options.Deps that always
// returns d, err — the fixture each test wires so the check(s) under
// test see exactly the Deps the test controls (fresh Previous is then
// stamped on by RunCycle itself).
func fakeDeps(d check.Deps, err error) func(context.Context) (check.Deps, error) {
	return func(context.Context) (check.Deps, error) { return d, err }
}

// recordingCheck builds a check.Check that captures the Deps it was
// called with into *got, so a cycle test can assert on Deps.Previous
// after RunCycle returns.
func recordingCheck(id string, node config.Node, cadence time.Duration, got *check.Deps) check.Check {
	return check.Check{
		ID:      id,
		Nodes:   []config.Node{node},
		Cadence: cadence,
		Run: func(_ context.Context, d check.Deps) (check.Result, error) {
			*got = d
			return check.Result{}, nil
		},
	}
}

// noopCheck builds a check.Check that runs and reports nothing.
func noopCheck(id string, node config.Node, cadence time.Duration) check.Check {
	return check.Check{
		ID:      id,
		Nodes:   []config.Node{node},
		Cadence: cadence,
		Run:     func(context.Context, check.Deps) (check.Result, error) { return check.Result{}, nil },
	}
}

// registryWith builds a *check.Registry containing checks, panicking on a
// duplicate id (a test-fixture bug, not a runtime condition).
func registryWith(checks ...check.Check) *check.Registry {
	r := check.NewRegistry()
	for _, c := range checks {
		if err := r.Register(c); err != nil {
			panic(err)
		}
	}
	return r
}

// testPeerClient is a peer.Client test double used where peer.Fake isn't
// enough: peer.Fake only records a formatted call string, not the actual
// argument, so it can't assert on a Heartbeat's UptimeSeconds. It is safe
// for concurrent use (a scheduled job's goroutine calls Heartbeat while
// the test goroutine reads back via Snapshot/Called), and — when Block is
// true — Heartbeat doesn't return until ctx is done, so a test can prove a
// scheduled job actually received a live, cancellable context rather than
// context.Background().
type testPeerClient struct {
	Block bool // when true, Heartbeat blocks until ctx.Done()

	mu         sync.Mutex
	heartbeats []peer.Heartbeat
	called     chan struct{}
	once       sync.Once
}

var _ peer.Client = (*testPeerClient)(nil)

func newTestPeerClient(block bool) *testPeerClient {
	return &testPeerClient{Block: block, called: make(chan struct{})}
}

func (p *testPeerClient) PushReport(context.Context, peer.ReportEnvelope) (peer.Ack, error) {
	return peer.Ack{}, nil
}

func (p *testPeerClient) FetchLatest(context.Context) (peer.ReportEnvelope, bool, error) {
	return peer.ReportEnvelope{}, false, nil
}

func (p *testPeerClient) SendDecision(context.Context, peer.Decision) (peer.Ack, error) {
	return peer.Ack{}, nil
}

// Heartbeat records hb, signals Called (once, on the first call), then —
// when Block is true — waits for ctx to be done before returning ctx.Err().
func (p *testPeerClient) Heartbeat(ctx context.Context, hb peer.Heartbeat) (peer.Ack, error) {
	p.mu.Lock()
	p.heartbeats = append(p.heartbeats, hb)
	p.mu.Unlock()
	p.once.Do(func() { close(p.called) })

	if p.Block {
		<-ctx.Done()
		return peer.Ack{}, ctx.Err()
	}
	return peer.Ack{}, nil
}

// Called is closed once the first Heartbeat call has been recorded.
func (p *testPeerClient) Called() <-chan struct{} { return p.called }

// Heartbeats returns a snapshot of every Heartbeat call recorded so far.
func (p *testPeerClient) Heartbeats() []peer.Heartbeat {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]peer.Heartbeat(nil), p.heartbeats...)
}
