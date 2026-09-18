package arr

import (
	"context"
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/prowlarr"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

func TestArrHealth(t *testing.T) {
	c := Checks(config.Config{})[0] // arr_health is the family's first check

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "healthy - no findings",
			deps: baseDeps(config.Config{}, &sonarr.Fake{}, &radarr.Fake{}, &prowlarr.Fake{}),
			want: nil,
		},
		{
			name: "warning item",
			deps: baseDeps(config.Config{}, &sonarr.Fake{HealthItems: []sonarr.HealthItem{
				{Type: "warning", Source: "IndexerStatusCheck", Message: "indexer x is unavailable"},
			}}, nil, nil),
			want: []wantFinding{{key: "sonarr:IndexerStatusCheck:" + shortHash("indexer x is unavailable"), sev: check.SeverityWarn}},
		},
		{
			name: "error item is critical",
			deps: baseDeps(config.Config{}, &sonarr.Fake{HealthItems: []sonarr.HealthItem{
				{Type: "error", Source: "AppDataCheck", Message: "app data folder is missing"},
			}}, nil, nil),
			want: []wantFinding{{key: "sonarr:AppDataCheck:" + shortHash("app data folder is missing"), sev: check.SeverityCritical}},
		},
		{
			name: "error type is case-insensitive",
			deps: baseDeps(config.Config{}, nil, &radarr.Fake{HealthItems: []radarr.HealthItem{
				{Type: "Error", Source: "RootFolderCheck", Message: "root folder is missing"},
			}}, nil),
			want: []wantFinding{{key: "radarr:RootFolderCheck:" + shortHash("root folder is missing"), sev: check.SeverityCritical}},
		},
		{
			name: "UpdateCheck items are excluded",
			deps: baseDeps(config.Config{}, &sonarr.Fake{HealthItems: []sonarr.HealthItem{
				{Type: "warning", Source: "UpdateCheck", Message: "New update is available"},
			}}, nil, nil),
			want: nil,
		},
		{
			name: "prowlarr item",
			deps: baseDeps(config.Config{}, nil, nil, &prowlarr.Fake{HealthItems: []prowlarr.HealthItem{
				{Type: "warning", Source: "IndexerLongTermStatusCheck", Message: "indexers are failing"},
			}}),
			want: []wantFinding{{key: "prowlarr:IndexerLongTermStatusCheck:" + shortHash("indexers are failing"), sev: check.SeverityWarn}},
		},
		{
			name:    "sonarr client error is a critical unreachable finding",
			deps:    baseDeps(config.Config{}, &sonarr.Fake{Err: errors.New("connection refused")}, nil, nil),
			want:    []wantFinding{{key: "sonarr", sev: check.SeverityCritical}},
			wantErr: nil,
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

func TestArrHealthUnreachableSummary(t *testing.T) {
	c := Checks(config.Config{})[0]
	deps := baseDeps(config.Config{}, &sonarr.Fake{Err: errors.New("connection refused")}, nil, nil)
	res, err := c.Run(context.Background(), deps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want 1", res.Findings)
	}
	f := res.Findings[0]
	want := "sonarr unreachable: connection refused"
	if f.Summary != want {
		t.Fatalf("summary = %q, want %q", f.Summary, want)
	}
}

func TestShortHash(t *testing.T) {
	a := shortHash("some message")
	b := shortHash("some message")
	c := shortHash("a different message")
	if a != b {
		t.Fatalf("shortHash not stable: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("shortHash collided for different inputs")
	}
	if len(a) != 8 {
		t.Fatalf("shortHash length = %d, want 8", len(a))
	}
}
