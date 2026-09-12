package prowlarr

import (
	"context"
	"fmt"
)

// Fake is an in-memory Client for tests and the check engine's table tests.
type Fake struct {
	HealthItems []HealthItem
	IndexerList []Indexer
	Statuses    []IndexerStatus
	Calls       []string
	Err         error
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

func (f *Fake) Health(context.Context) ([]HealthItem, error) {
	return f.HealthItems, f.record("Health()")
}

func (f *Fake) Indexers(context.Context) ([]Indexer, error) {
	return f.IndexerList, f.record("Indexers()")
}

func (f *Fake) IndexerStatus(context.Context) ([]IndexerStatus, error) {
	return f.Statuses, f.record("IndexerStatus()")
}

func (f *Fake) DeleteIndexer(_ context.Context, id int64) error {
	return f.record("DeleteIndexer(%d)", id)
}

func (f *Fake) TestIndexer(_ context.Context, id int64) error {
	return f.record("TestIndexer(%d)", id)
}
