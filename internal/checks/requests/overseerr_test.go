package requests

import (
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
	"github.com/parsoFish/healarr/internal/config"
)

// stuckAfterCfg builds a config.Config carrying only the
// overseerr_stuck_processing threshold.
func stuckAfterCfg(after time.Duration) config.Config {
	return config.Config{Checks: config.Checks{OverseerrStuckAfter: after}}
}

func TestOverseerrStuckProcessing(t *testing.T) {
	c := Checks(config.Config{})[1] // overseerr_stuck_processing is the family's second check
	cfg := stuckAfterCfg(7 * 24 * time.Hour)

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "stuck",
			deps: baseDeps(cfg, nil, &overseerr.Fake{RequestList: []overseerr.Request{
				{ID: 42, MediaType: "movie", TMDBID: 603, RequestedBy: "dave@example.com", UpdatedAt: fixedNow.Add(-10 * 24 * time.Hour)},
			}}),
			want: []wantFinding{{key: "overseerr:42", sev: check.SeverityWarn}},
		},
		{
			name: "fresh",
			deps: baseDeps(cfg, nil, &overseerr.Fake{RequestList: []overseerr.Request{
				{ID: 7, MediaType: "tv", TMDBID: 1399, RequestedBy: "dave@example.com", UpdatedAt: fixedNow.Add(-1 * time.Hour)},
			}}),
			want: nil,
		},
		{
			name:    "requests error",
			deps:    baseDeps(cfg, nil, &overseerr.Fake{Err: errors.New("overseerr down")}),
			wantErr: errAny,
		},
		{
			name:    "not configured",
			deps:    baseDeps(cfg, nil, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, c, tt.deps, tt.want, tt.wantErr)
			switch tt.name {
			case "stuck":
				if got := res.Metrics["overseerr_processing"]; got != 1 {
					t.Fatalf("overseerr_processing = %v, want 1", got)
				}
				want := "overseerr request #42 (movie tmdb:603) processing since " +
					fixedNow.Add(-10*24*time.Hour).Format(time.RFC3339) + " for dave@example.com"
				if got := res.Findings[0].Summary; got != want {
					t.Fatalf("summary = %q, want %q", got, want)
				}
			case "fresh":
				if got := res.Metrics["overseerr_processing"]; got != 1 {
					t.Fatalf("overseerr_processing = %v, want 1", got)
				}
			}
		})
	}
}
