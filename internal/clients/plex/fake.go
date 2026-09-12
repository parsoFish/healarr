package plex

import (
	"context"
	"fmt"
)

// Fake is an in-memory Client for tests and the check engine's table tests.
type Fake struct {
	IdentityValue Identity
	LibraryList   []Library
	ItemsByKey    map[string][]Item
	Calls         []string
	Err           error
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

func (f *Fake) Identity(context.Context) (Identity, error) {
	return f.IdentityValue, f.record("Identity()")
}

func (f *Fake) Libraries(context.Context) ([]Library, error) {
	return f.LibraryList, f.record("Libraries()")
}

func (f *Fake) RecentlyAdded(_ context.Context, libraryKey string, limit int) ([]Item, error) {
	return f.ItemsByKey[libraryKey], f.record("RecentlyAdded(%s,%d)", libraryKey, limit)
}

func (f *Fake) RefreshLibrary(_ context.Context, libraryKey string) error {
	return f.record("RefreshLibrary(%s)", libraryKey)
}
