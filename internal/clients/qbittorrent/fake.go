package qbittorrent

import (
	"context"
	"fmt"
)

// Fake is an in-memory Client for tests and the check engine's table tests.
type Fake struct {
	TorrentList []Torrent
	FilesByHash map[string][]File
	Prefs       Preferences
	Calls       []string
	Err         error
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

func (f *Fake) Version(context.Context) (string, error) {
	return "fake", f.record("Version()")
}

func (f *Fake) Torrents(_ context.Context, filter, category string) ([]Torrent, error) {
	return f.TorrentList, f.record("Torrents(%q, %q)", filter, category)
}

func (f *Fake) Files(_ context.Context, hash string) ([]File, error) {
	return f.FilesByHash[hash], f.record("Files(%s)", hash)
}

func (f *Fake) Delete(_ context.Context, hashes []string, deleteFiles bool) error {
	return f.record("Delete(%v, %t)", hashes, deleteFiles)
}

func (f *Fake) Reannounce(_ context.Context, hashes []string) error {
	return f.record("Reannounce(%v)", hashes)
}

func (f *Fake) Resume(_ context.Context, hashes []string) error {
	return f.record("Resume(%v)", hashes)
}

func (f *Fake) Preferences(context.Context) (Preferences, error) {
	return f.Prefs, f.record("Preferences()")
}

func (f *Fake) SetPreferences(_ context.Context, patch map[string]any) error {
	return f.record("SetPreferences(%v)", patch)
}
