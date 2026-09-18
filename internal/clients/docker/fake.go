package docker

import (
	"context"
	"fmt"
	"strings"
)

// Fake is an in-memory Client for tests.
type Fake struct {
	List       []Container
	LogsByName map[string]string
	Usage      DiskUsage
	ExecOut    map[string]string
	// ExecOutByCmd, when set, overrides ExecOut for an exact command. Key is
	// container + " " + strings.Join(args, " ").
	ExecOutByCmd map[string]string
	Calls        []string
	Err          error
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

func (f *Fake) Containers(context.Context) ([]Container, error) {
	return f.List, f.record("Containers()")
}

func (f *Fake) Logs(_ context.Context, name string, tail int) (string, error) {
	return f.LogsByName[name], f.record("Logs(%s,%d)", name, tail)
}

func (f *Fake) DiskUsage(context.Context) (DiskUsage, error) {
	return f.Usage, f.record("DiskUsage()")
}

func (f *Fake) PruneImages(_ context.Context, dangling bool) (int64, error) {
	return f.Usage.ImagesReclaimable, f.record("PruneImages(%t)", dangling)
}

func (f *Fake) Exec(_ context.Context, container string, args ...string) (string, error) {
	key := container + " " + strings.Join(args, " ")
	if out, ok := f.ExecOutByCmd[key]; ok {
		return out, f.record("Exec(%s, %s)", container, strings.Join(args, " "))
	}
	return f.ExecOut[container], f.record("Exec(%s, %s)", container, strings.Join(args, " "))
}
