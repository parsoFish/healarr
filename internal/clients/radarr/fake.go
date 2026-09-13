package radarr

import (
	"context"
	"fmt"
	"time"
)

// Fake is an in-memory Client for tests and the check engine's table tests.
type Fake struct {
	HealthItems    []HealthItem
	QueueItems     []QueueItem
	MovieList      []Movie
	Roots          []RootFolder
	Missing        int
	HistoryRecords []HistoryRecord
	Calls          []string
	Err            error
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

func (f *Fake) Health(context.Context) ([]HealthItem, error) {
	return f.HealthItems, f.record("Health()")
}
func (f *Fake) Queue(context.Context) ([]QueueItem, error) { return f.QueueItems, f.record("Queue()") }
func (f *Fake) Movies(context.Context) ([]Movie, error)    { return f.MovieList, f.record("Movies()") }
func (f *Fake) RootFolders(context.Context) ([]RootFolder, error) {
	return f.Roots, f.record("RootFolders()")
}
func (f *Fake) WantedMissingCount(context.Context) (int, error) {
	return f.Missing, f.record("WantedMissingCount()")
}
func (f *Fake) History(_ context.Context, since time.Time, eventType string) ([]HistoryRecord, error) {
	var out []HistoryRecord
	for _, r := range f.HistoryRecords {
		if (since.IsZero() || !r.Date.Before(since)) && (eventType == "" || r.EventType == eventType) {
			out = append(out, r)
		}
	}
	return out, f.record("History(%s,%s)", since.Format(time.RFC3339), eventType)
}
func (f *Fake) DeleteQueueItem(_ context.Context, id int64, rm, bl bool) error {
	return f.record("DeleteQueueItem(%d,%t,%t)", id, rm, bl)
}
func (f *Fake) UpdateMovieMonitored(_ context.Context, id int64, m bool) error {
	return f.record("UpdateMovieMonitored(%d,%t)", id, m)
}
func (f *Fake) DeleteMovie(_ context.Context, id int64, df, ex bool) error {
	return f.record("DeleteMovie(%d,%t,%t)", id, df, ex)
}
func (f *Fake) RunCommand(_ context.Context, name string, _ map[string]any) (int64, error) {
	return 1, f.record("RunCommand(%s)", name)
}
