package peer

import (
	"context"
	"fmt"
)

// Fake is an in-memory Client for tests: it records every call and returns
// a caller-configured Ack/ReportEnvelope/error from every method.
type Fake struct {
	Ack       Ack
	Envelope  ReportEnvelope
	HasLatest bool // FetchLatest's ok return value
	Err       error
	Calls     []string
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

// PushReport records the call and returns the configured Ack/Err.
func (f *Fake) PushReport(_ context.Context, env ReportEnvelope) (Ack, error) {
	return f.Ack, f.record("PushReport(%s)", env.Node)
}

// FetchLatest records the call and returns the configured
// Envelope/HasLatest/Err.
func (f *Fake) FetchLatest(_ context.Context) (ReportEnvelope, bool, error) {
	err := f.record("FetchLatest()")
	if err != nil {
		return ReportEnvelope{}, false, err
	}
	return f.Envelope, f.HasLatest, nil
}

// SendDecision records the call and returns the configured Ack/Err.
func (f *Fake) SendDecision(_ context.Context, d Decision) (Ack, error) {
	return f.Ack, f.record("SendDecision(%s,%s)", d.Kind, d.EntityKey)
}

// Heartbeat records the call and returns the configured Ack/Err.
func (f *Fake) Heartbeat(_ context.Context, hb Heartbeat) (Ack, error) {
	return f.Ack, f.record("Heartbeat(%s)", hb.Node)
}
