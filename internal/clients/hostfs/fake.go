package hostfs

import (
	"context"
	"fmt"
	"path/filepath"
)

// Fake is an in-memory Client for tests.
type Fake struct {
	MountList []Mount
	Devices   map[string]uint64
	Usages    map[string]Usage
	Sizes     map[string]int64
	Entries   map[string][]Entry
	// Removed records every path Remove was called with, in call order,
	// so a test can assert exactly what got deleted.
	Removed []string
	Calls   []string
	Err     error
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

// Mounts returns the configured MountList.
func (f *Fake) Mounts(context.Context) ([]Mount, error) {
	return f.MountList, f.record("Mounts()")
}

// IsMountpoint reports "/" as always mounted, and otherwise compares
// path's configured device against its parent's, mirroring OS.
func (f *Fake) IsMountpoint(_ context.Context, path string) (bool, error) {
	if err := f.record("IsMountpoint(%s)", path); err != nil {
		return false, err
	}
	if path == "/" {
		return true, nil
	}
	return f.Devices[path] != f.Devices[filepath.Dir(path)], nil
}

// DeviceID returns the configured device for path.
func (f *Fake) DeviceID(_ context.Context, path string) (uint64, error) {
	return f.Devices[path], f.record("DeviceID(%s)", path)
}

// Usage returns the configured Usage for path.
func (f *Fake) Usage(_ context.Context, path string) (Usage, error) {
	return f.Usages[path], f.record("Usage(%s)", path)
}

// DirSize returns the configured size for path.
func (f *Fake) DirSize(_ context.Context, path string, maxDepth int) (int64, error) {
	return f.Sizes[path], f.record("DirSize(%s,%d)", path, maxDepth)
}

// ListDir returns the configured entries for path.
func (f *Fake) ListDir(_ context.Context, path string) ([]Entry, error) {
	return f.Entries[path], f.record("ListDir(%s)", path)
}

// Remove records path in Removed unless Err is set, mirroring OS.Remove's
// contract without touching a real filesystem.
func (f *Fake) Remove(_ context.Context, path string) error {
	if err := f.record("Remove(%s)", path); err != nil {
		return err
	}
	f.Removed = append(f.Removed, path)
	return nil
}
