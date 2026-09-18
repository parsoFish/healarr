package arr

import (
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/prowlarr"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

func TestServiceUpdateAvailable(t *testing.T) {
	c := Checks(config.Config{})[3] // service_update_available is the family's fourth check

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "no update - no findings",
			deps: baseDeps(config.Config{}, &sonarr.Fake{}, &radarr.Fake{}, &prowlarr.Fake{}),
			want: nil,
		},
		{
			name: "sonarr update available",
			deps: baseDeps(config.Config{}, &sonarr.Fake{HealthItems: []sonarr.HealthItem{
				{Type: "notice", Source: "UpdateCheck", Message: "New update is available"},
			}}, nil, nil),
			want: []wantFinding{{key: "sonarr:update", sev: check.SeverityInfo}},
		},
		{
			name: "radarr update available",
			deps: baseDeps(config.Config{}, nil, &radarr.Fake{HealthItems: []radarr.HealthItem{
				{Type: "notice", Source: "UpdateCheck", Message: "New update is available"},
			}}, nil),
			want: []wantFinding{{key: "radarr:update", sev: check.SeverityInfo}},
		},
		{
			name: "prowlarr update available",
			deps: baseDeps(config.Config{}, nil, nil, &prowlarr.Fake{HealthItems: []prowlarr.HealthItem{
				{Type: "notice", Source: "UpdateCheck", Message: "New update is available"},
			}}),
			want: []wantFinding{{key: "prowlarr:update", sev: check.SeverityInfo}},
		},
		{
			name: "non-update items are ignored",
			deps: baseDeps(config.Config{}, &sonarr.Fake{HealthItems: []sonarr.HealthItem{
				{Type: "warning", Source: "IndexerStatusCheck", Message: "indexer unavailable"},
			}}, nil, nil),
			want: nil,
		},
		{
			name:    "client error returns error, not a finding",
			deps:    baseDeps(config.Config{}, &sonarr.Fake{Err: errors.New("connection refused")}, nil, nil),
			wantErr: errAny,
		},
		{
			name:    "not configured",
			deps:    baseDeps(config.Config{}, nil, nil, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run(t, c, tt.deps, tt.want, tt.wantErr)
		})
	}
}
