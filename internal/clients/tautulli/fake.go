package tautulli

import (
	"context"
	"fmt"
	"time"
)

// Fake is an in-memory Client for tests and the check engine's table tests.
type Fake struct {
	HistoryRows []HistoryRow
	Streams     int
	Calls       []string
	Err         error
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

func (f *Fake) Ping(context.Context) error {
	return f.record("Ping()")
}

func (f *Fake) History(_ context.Context, since time.Time, length int) ([]HistoryRow, error) {
	return f.HistoryRows, f.record("History(%s,%d)", since.Format(time.RFC3339), length)
}

func (f *Fake) ActivityCount(context.Context) (int, error) {
	return f.Streams, f.record("ActivityCount()")
}
