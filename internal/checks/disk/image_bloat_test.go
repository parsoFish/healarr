package disk

import (
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/docker"
	"github.com/parsoFish/healarr/internal/config"
)

func TestDockerImageBloat(t *testing.T) {
	c := Checks(config.Config{})[2] // docker_image_bloat
	cfg := config.Config{Checks: config.Checks{ImageBloatWarnGB: 2}}

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "healthy",
			deps: baseDeps(cfg, nil, &docker.Fake{Usage: docker.DiskUsage{ImagesReclaimable: 500e6}}, nil),
			want: nil,
		},
		{
			name: "warn",
			deps: baseDeps(cfg, nil, &docker.Fake{Usage: docker.DiskUsage{ImagesReclaimable: 3e9}}, nil),
			want: []wantFinding{{key: "docker:images", sev: check.SeverityWarn}},
		},
		{
			name:    "disk usage error",
			deps:    baseDeps(cfg, nil, &docker.Fake{Err: errors.New("docker socket unavailable")}, nil),
			wantErr: errAny,
		},
		{
			name:    "not configured",
			deps:    baseDeps(cfg, nil, nil, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, c, tt.deps, tt.want, tt.wantErr)
			if tt.name == "warn" {
				if got := res.Metrics["docker_images_reclaimable_bytes"]; got != 3e9 {
					t.Fatalf("docker_images_reclaimable_bytes metric = %v, want 3e9", got)
				}
			}
		})
	}
}
