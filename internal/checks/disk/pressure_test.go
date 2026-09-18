package disk

import (
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/config"
)

func testDiskPressureCfg(paths []string) config.Config {
	return config.Config{Checks: config.Checks{DiskPaths: paths, DiskWarnPercent: 85, DiskCritPercent: 92}}
}

func TestDiskPressurePiSD(t *testing.T) {
	c := Checks(config.Config{})[0] // disk_pressure_pi_sd
	cfg := testDiskPressureCfg([]string{"/"})

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "healthy",
			deps: baseDeps(cfg, &hostfs.Fake{Usages: map[string]hostfs.Usage{"/": {UsedPercent: 40, Free: 100e9}}}, nil, nil),
			want: nil,
		},
		{
			name: "warn",
			deps: baseDeps(cfg, &hostfs.Fake{Usages: map[string]hostfs.Usage{"/": {UsedPercent: 90, Free: 5e9}}}, nil, nil),
			want: []wantFinding{{key: "/", sev: check.SeverityWarn}},
		},
		{
			name: "critical",
			deps: baseDeps(cfg, &hostfs.Fake{Usages: map[string]hostfs.Usage{"/": {UsedPercent: 95, Free: 1e9}}}, nil, nil),
			want: []wantFinding{{key: "/", sev: check.SeverityCritical}},
		},
		{
			name:    "usage error",
			deps:    baseDeps(cfg, &hostfs.Fake{Err: errors.New("statfs: no such file or directory")}, nil, nil),
			wantErr: errAny,
		},
		{
			name:    "not configured: no paths",
			deps:    baseDeps(config.Config{}, &hostfs.Fake{}, nil, nil),
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "not configured: no host client",
			deps:    baseDeps(cfg, nil, nil, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, c, tt.deps, tt.want, tt.wantErr)
			if tt.name == "warn" {
				if got := res.Metrics["disk_used_percent:/"]; got != 90 {
					t.Fatalf("disk_used_percent metric = %v, want 90", got)
				}
			}
		})
	}
}

// TestDiskPressureNASVolume checks the second catalogue row wires the same
// pressure() logic with its own tier (correct, not nudge) and confirms the
// NAS-side path/threshold values (set by the NAS's own config file) drive
// it, not the Pi row's.
func TestDiskPressureNASVolume(t *testing.T) {
	c := Checks(config.Config{})[1] // disk_pressure_nas_volume
	cfg := testDiskPressureCfg([]string{"/mnt/data"})
	deps := baseDeps(cfg, &hostfs.Fake{Usages: map[string]hostfs.Usage{"/mnt/data": {UsedPercent: 93, Free: 1e9}}}, nil, nil)

	res := run(t, c, deps, []wantFinding{{key: "/mnt/data", sev: check.SeverityCritical}}, nil)
	if res.Findings[0].Tier != check.TierCorrect {
		t.Fatalf("Tier = %s, want %s", res.Findings[0].Tier, check.TierCorrect)
	}
}
