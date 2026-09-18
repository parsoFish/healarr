package mounts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/docker"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/config"
)

// usageFailHost delegates to an embedded *hostfs.Fake for everything except
// Usage, which always fails. hostfs.Fake's single Err field fails every
// method uniformly, so it can't express "IsMountpoint succeeds, Usage
// fails" on its own.
type usageFailHost struct {
	*hostfs.Fake
	usageErr error
}

func (h usageFailHost) Usage(_ context.Context, path string) (hostfs.Usage, error) {
	h.Calls = append(h.Calls, "Usage("+path+")")
	return hostfs.Usage{}, h.usageErr
}

func TestHostMountHealth(t *testing.T) {
	c := Checks(config.Config{})[1] // host_mount_health is the family's second check
	singleMount := mountsCfg(config.Mount{Host: "/mnt/nas/tv", Container: "sonarr", ContainerPath: "/tv"})
	healthyDevices := map[string]uint64{"/mnt/nas/tv": 41, "/mnt/nas": 2049}
	notMountedDevices := map[string]uint64{"/mnt/nas/tv": 2049, "/mnt/nas": 2049}

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "mounted and usage ok",
			deps: baseDeps(singleMount, &docker.Fake{}, &hostfs.Fake{
				Devices: healthyDevices,
				Usages:  map[string]hostfs.Usage{"/mnt/nas/tv": {Path: "/mnt/nas/tv", Total: 1000, Used: 400, Free: 600, UsedPercent: 40}},
			}),
			want: nil,
		},
		{
			name: "not mountpoint",
			deps: baseDeps(singleMount, &docker.Fake{}, &hostfs.Fake{Devices: notMountedDevices}),
			want: []wantFinding{{key: "/mnt/nas/tv", sev: check.SeverityCritical}},
		},
		{
			name: "usage error",
			deps: baseDeps(singleMount, &docker.Fake{}, usageFailHost{
				Fake:     &hostfs.Fake{Devices: healthyDevices},
				usageErr: errors.New("stale file handle"),
			}),
			want: []wantFinding{{key: "/mnt/nas/tv", sev: check.SeverityCritical}},
		},
		{
			name:    "is mountpoint error",
			deps:    baseDeps(singleMount, &docker.Fake{}, &hostfs.Fake{Err: errors.New("nas offline")}),
			wantErr: errAny,
		},
		{
			name:    "not configured",
			deps:    baseDeps(config.Config{}, &docker.Fake{}, &hostfs.Fake{}),
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "host client not configured",
			deps:    baseDeps(singleMount, &docker.Fake{}, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, c, tt.deps, tt.want, tt.wantErr)
			if tt.name == "mounted and usage ok" {
				if got := res.Metrics["mount_used_percent:/mnt/nas/tv"]; got != 40 {
					t.Fatalf("mount_used_percent metric = %v, want 40", got)
				}
			}
		})
	}
}

func TestHostMountHealthDedupesHostPaths(t *testing.T) {
	c := Checks(config.Config{})[1]
	cfg := mountsCfg(
		config.Mount{Host: "/mnt/nas/tv", Container: "sonarr", ContainerPath: "/tv"},
		config.Mount{Host: "/mnt/nas/tv", Container: "radarr", ContainerPath: "/tv"},
	)
	host := &hostfs.Fake{
		Devices: map[string]uint64{"/mnt/nas/tv": 41, "/mnt/nas": 2049},
		Usages:  map[string]hostfs.Usage{"/mnt/nas/tv": {UsedPercent: 10}},
	}

	if _, err := c.Run(context.Background(), baseDeps(cfg, &docker.Fake{}, host)()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	isMountCalls := 0
	for _, call := range host.Calls {
		if strings.HasPrefix(call, "IsMountpoint(") {
			isMountCalls++
		}
	}
	if isMountCalls != 1 {
		t.Fatalf("IsMountpoint calls = %d, want 1; calls=%v", isMountCalls, host.Calls)
	}
}
