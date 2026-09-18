package arr

import (
	"context"
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

func wantedCfg() config.Config {
	return config.Config{Checks: config.Checks{WantedSpikePercent: 20, WantedSpikeMin: 10}}
}

func TestArrWantedMissingSpike(t *testing.T) {
	c := Checks(config.Config{})[2] // arr_wanted_missing_spike is the family's third check

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "first run - metric only, no finding",
			deps: baseDeps(wantedCfg(), &sonarr.Fake{Missing: 50}, nil, nil),
			want: nil,
		},
		{
			name: "spike clears both delta and percent thresholds",
			deps: withPrevious(baseDeps(wantedCfg(), &sonarr.Fake{Missing: 60}, nil, nil),
				map[string]float64{"sonarr_wanted_missing": 40}),
			want: []wantFinding{{key: "sonarr:wanted", sev: check.SeverityWarn}},
		},
		{
			name: "delta below min - no finding even with high percent",
			deps: withPrevious(baseDeps(wantedCfg(), &sonarr.Fake{Missing: 4}, nil, nil),
				map[string]float64{"sonarr_wanted_missing": 2}),
			want: nil,
		},
		{
			name: "delta clears min but percent below threshold",
			deps: withPrevious(baseDeps(wantedCfg(), &sonarr.Fake{Missing: 450}, nil, nil),
				map[string]float64{"sonarr_wanted_missing": 440}),
			want: nil,
		},
		{
			name: "decrease - no finding",
			deps: withPrevious(baseDeps(wantedCfg(), &sonarr.Fake{Missing: 50}, nil, nil),
				map[string]float64{"sonarr_wanted_missing": 60}),
			want: nil,
		},
		{
			name: "zero previous baseline - percent check skipped",
			deps: withPrevious(baseDeps(wantedCfg(), &sonarr.Fake{Missing: 10}, nil, nil),
				map[string]float64{"sonarr_wanted_missing": 0}),
			want: []wantFinding{{key: "sonarr:wanted", sev: check.SeverityWarn}},
		},
		{
			name: "radarr spike",
			deps: withPrevious(baseDeps(wantedCfg(), nil, &radarr.Fake{Missing: 60}, nil),
				map[string]float64{"radarr_wanted_missing": 40}),
			want: []wantFinding{{key: "radarr:wanted", sev: check.SeverityWarn}},
		},
		{
			name:    "client error",
			deps:    baseDeps(wantedCfg(), &sonarr.Fake{Err: errors.New("connection refused")}, nil, nil),
			wantErr: errAny,
		},
		{
			name:    "not configured",
			deps:    baseDeps(wantedCfg(), nil, nil, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run(t, c, tt.deps, tt.want, tt.wantErr)
		})
	}
}

func TestArrWantedMissingSpikeMetric(t *testing.T) {
	c := Checks(config.Config{})[2]
	deps := baseDeps(wantedCfg(), &sonarr.Fake{Missing: 50}, &radarr.Fake{Missing: 12}, nil)
	res, err := c.Run(context.Background(), deps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Metrics["sonarr_wanted_missing"] != 50 {
		t.Fatalf("sonarr_wanted_missing = %v, want 50", res.Metrics["sonarr_wanted_missing"])
	}
	if res.Metrics["radarr_wanted_missing"] != 12 {
		t.Fatalf("radarr_wanted_missing = %v, want 12", res.Metrics["radarr_wanted_missing"])
	}
}

func TestArrWantedMissingSpikeFindingData(t *testing.T) {
	c := Checks(config.Config{})[2]
	deps := withPrevious(baseDeps(wantedCfg(), &sonarr.Fake{Missing: 60}, nil, nil),
		map[string]float64{"sonarr_wanted_missing": 40})
	res, err := c.Run(context.Background(), deps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want 1", res.Findings)
	}
	f := res.Findings[0]
	wantSummary := "sonarr: wanted/missing jumped from 40 to 60"
	if f.Summary != wantSummary {
		t.Fatalf("summary = %q, want %q", f.Summary, wantSummary)
	}
	if f.Data["previous"] != float64(40) || f.Data["current"] != 60 {
		t.Fatalf("data = %+v, want previous=40 current=60", f.Data)
	}
}
