package decision

import (
	"context"
	"sync"
	"time"

	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
)

// markCall is one MarkDecision invocation fakeDecisionStore recorded.
type markCall struct {
	ID     int64
	Status string
	At     time.Time
	Cause  error
}

// snoozeCall is one SnoozeEntity invocation fakeDecisionStore recorded.
type snoozeCall struct {
	EntityKey string
	Until     time.Time
}

// fakeDecisionStore is an in-memory DecisionStore for tests: it records
// every call and returns caller-configured data/errors, mirroring how
// *store.Store satisfies the same interface.
type fakeDecisionStore struct {
	Decisions map[int64]store.Decision
	ByIDErr   error

	MarkErr   error
	MarkCalls []markCall

	SnoozeErr   error
	SnoozeCalls []snoozeCall

	RecordErr    error
	Remediations []store.Remediation
	nextRemID    int64
}

var _ DecisionStore = (*fakeDecisionStore)(nil)

func (f *fakeDecisionStore) DecisionByID(_ context.Context, id int64) (store.Decision, bool, error) {
	if f.ByIDErr != nil {
		return store.Decision{}, false, f.ByIDErr
	}
	d, ok := f.Decisions[id]
	return d, ok, nil
}

func (f *fakeDecisionStore) MarkDecision(_ context.Context, id int64, status string, at time.Time, cause error) error {
	f.MarkCalls = append(f.MarkCalls, markCall{ID: id, Status: status, At: at, Cause: cause})
	return f.MarkErr
}

func (f *fakeDecisionStore) SnoozeEntity(_ context.Context, entityKey string, until time.Time) error {
	f.SnoozeCalls = append(f.SnoozeCalls, snoozeCall{EntityKey: entityKey, Until: until})
	return f.SnoozeErr
}

func (f *fakeDecisionStore) RecordRemediation(_ context.Context, r store.Remediation) (int64, error) {
	f.Remediations = append(f.Remediations, r)
	if f.RecordErr != nil {
		return 0, f.RecordErr
	}
	f.nextRemID++
	return f.nextRemID, nil
}

// fakePeerClient is a peer.Client test double that captures the full
// peer.Decision SendDecision was called with (peer.Fake only records a
// formatted "Kind,EntityKey" string, not the Payload, so it can't assert
// on the hashes decision.Execute sends).
type fakePeerClient struct {
	SendErr error

	mu   sync.Mutex
	sent []peer.Decision
}

var _ peer.Client = (*fakePeerClient)(nil)

func (p *fakePeerClient) PushReport(context.Context, peer.ReportEnvelope) (peer.Ack, error) {
	return peer.Ack{}, nil
}

func (p *fakePeerClient) FetchLatest(context.Context) (peer.ReportEnvelope, bool, error) {
	return peer.ReportEnvelope{}, false, nil
}

func (p *fakePeerClient) SendDecision(_ context.Context, d peer.Decision) (peer.Ack, error) {
	p.mu.Lock()
	p.sent = append(p.sent, d)
	p.mu.Unlock()
	if p.SendErr != nil {
		return peer.Ack{}, p.SendErr
	}
	return peer.Ack{Status: "ok"}, nil
}

func (p *fakePeerClient) Heartbeat(context.Context, peer.Heartbeat) (peer.Ack, error) {
	return peer.Ack{}, nil
}

// Sent returns a snapshot of every SendDecision call recorded so far.
func (p *fakePeerClient) Sent() []peer.Decision {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]peer.Decision(nil), p.sent...)
}
