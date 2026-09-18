package plex

import (
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/plex"
	"github.com/parsoFish/healarr/internal/config"
)

func TestPlexReachability(t *testing.T) {
	c := Checks(config.Config{})[0] // plex_reachability is the family's first check

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "reachable",
			deps: baseDeps(config.Config{}, &plex.Fake{IdentityValue: plex.Identity{Version: "1.40.0"}}, nil),
			want: nil,
		},
		{
			name: "identity fails",
			deps: baseDeps(config.Config{}, &plex.Fake{Err: errors.New("connection refused")}, nil),
			want: []wantFinding{{key: "plex", sev: check.SeverityCritical}},
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
				if got := res.Metrics["plex_reachable"]; got != 1 {
					t.Fatalf("plex_reachable = %v, want 1", got)
				}
			case "identity fails":
				if len(res.Findings) != 1 {
					t.Fatalf("findings = %+v, want 1", res.Findings)
				}
				want := "plex unreachable: connection refused"
				if got := res.Findings[0].Summary; got != want {
					t.Fatalf("summary = %q, want %q", got, want)
				}
			}
		})
	}
}
