package disk

import (
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/config"
)

func TestLogSize(t *testing.T) {
	c := Checks(config.Config{})[3] // log_size
	cfg := config.Config{Checks: config.Checks{LogDirs: []string{"/var/log"}, LogWarnGB: 1}}

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "healthy",
			deps: baseDeps(cfg, &hostfs.Fake{Sizes: map[string]int64{"/var/log": 500e6}}, nil, nil),
			want: nil,
		},
		{
			name: "warn",
			deps: baseDeps(cfg, &hostfs.Fake{Sizes: map[string]int64{"/var/log": 2e9}}, nil, nil),
			want: []wantFinding{{key: "/var/log", sev: check.SeverityWarn}},
		},
		{
			name:    "dirsize error",
			deps:    baseDeps(cfg, &hostfs.Fake{Err: errors.New("permission denied")}, nil, nil),
			wantErr: errAny,
		},
		{
			name:    "not configured: no dirs",
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
				if got := res.Metrics["log_size_bytes:/var/log"]; got != 2e9 {
					t.Fatalf("log_size_bytes metric = %v, want 2e9", got)
				}
			}
		})
	}
}

// TestRecycleBinSize checks the recycle_bin_size row wires dirSize with the
// NAS's own dir list/threshold/label and tier (correct, not nudge).
func TestRecycleBinSize(t *testing.T) {
	c := Checks(config.Config{})[4] // recycle_bin_size
	cfg := config.Config{Checks: config.Checks{RecycleDirs: []string{"/mnt/data/#recycle"}, RecycleWarnGB: 20}}
	deps := baseDeps(cfg, &hostfs.Fake{Sizes: map[string]int64{"/mnt/data/#recycle": 25e9}}, nil, nil)

	res := run(t, c, deps, []wantFinding{{key: "/mnt/data/#recycle", sev: check.SeverityWarn}}, nil)
	if res.Findings[0].Tier != check.TierCorrect {
		t.Fatalf("Tier = %s, want %s", res.Findings[0].Tier, check.TierCorrect)
	}
	if got := res.Metrics["recycle_bin_size_bytes:/mnt/data/#recycle"]; got != 25e9 {
		t.Fatalf("recycle_bin_size_bytes metric = %v, want 25e9", got)
	}
}
