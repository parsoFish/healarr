package overseerr

import (
	"context"
	"fmt"
)

// Fake is an in-memory Client for tests and the check engine's table tests.
type Fake struct {
	StatusValue Status
	RequestList []Request
	Calls       []string
	Err         error
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

func (f *Fake) Status(context.Context) (Status, error) {
	return f.StatusValue, f.record("Status()")
}

func (f *Fake) Requests(_ context.Context, filter string) ([]Request, error) {
	return f.RequestList, f.record("Requests(%s)", filter)
}

func (f *Fake) DeclineRequest(_ context.Context, id int64) error {
	return f.record("DeclineRequest(%d)", id)
}
