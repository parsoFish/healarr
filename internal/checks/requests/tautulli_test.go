package requests

import (
	"context"
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/tautulli"
	"github.com/parsoFish/healarr/internal/config"
)

// activityCountFailTautulli delegates to an embedded *tautulli.Fake for
// Ping, but always fails ActivityCount. tautulli.Fake's single Err field
// fails every method uniformly, so it can't express "Ping succeeds,
// ActivityCount fails" on its own.
type activityCountFailTautulli struct {
	*tautulli.Fake
	activityErr error
}

func (t activityCountFailTautulli) ActivityCount(_ context.Context) (int, error) {
	t.Calls = append(t.Calls, "ActivityCount()")
	return 0, t.activityErr
}

func TestTautulliReachability(t *testing.T) {
	c := Checks(config.Config{})[0] // tautulli_reachability is the family's first check

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "reachable",
			deps: baseDeps(config.Config{}, &tautulli.Fake{Streams: 3}, nil),
			want: nil,
		},
		{
			name: "ping fails",
			deps: baseDeps(config.Config{}, &tautulli.Fake{Err: errors.New("connection refused")}, nil),
			want: []wantFinding{{key: "tautulli", sev: check.SeverityWarn}},
		},
		{
			name: "activity count fails",
			deps: baseDeps(config.Config{}, activityCountFailTautulli{
				Fake:        &tautulli.Fake{},
				activityErr: errors.New("bad response"),
			}, nil),
			wantErr: errAny,
		},
		{
			name:    "not configured",
			deps:    baseDeps(config.Config{}, nil, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, c, tt.deps, tt.want, tt.wantErr)
			switch tt.name {
			case "reachable":
				if got := res.Metrics["tautulli_active_streams"]; got != 3 {
					t.Fatalf("tautulli_active_streams = %v, want 3", got)
				}
			case "ping fails":
				if len(res.Findings) != 1 {
					t.Fatalf("findings = %+v, want 1", res.Findings)
				}
				want := "tautulli unreachable: connection refused"
				if got := res.Findings[0].Summary; got != want {
					t.Fatalf("summary = %q, want %q", got, want)
				}
			}
		})
	}
}
