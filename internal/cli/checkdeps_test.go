package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

func TestBuildCheckDepsGatesClientsByConfiguredURL(t *testing.T) {
	deps, _ := newTestDeps()
	cfg := config.Config{
		Node:     config.NodeNAS, // no docker socket configured: Docker stays nil too
		Services: config.Services{Sonarr: config.Service{URL: "http://sonarr:8989"}},
	}

	d, err := buildCheckDeps(context.Background(), deps, &GlobalFlags{}, cfg, config.Secrets{})
	if err != nil {
		t.Fatalf("buildCheckDeps: %v", err)
	}
	if d.Sonarr == nil {
		t.Error("expected Sonarr client to be built (URL configured)")
	}
	if d.Radarr != nil || d.Prowlarr != nil || d.QBit != nil || d.Plex != nil || d.Tautulli != nil || d.Overseerr != nil {
		t.Error("expected every unconfigured service client to stay nil")
	}
	if d.Docker != nil {
		t.Error("expected Docker to stay nil on nas with no socket configured")
	}
	if d.Host == nil {
		t.Error("expected Host to always be built")
	}
	if d.Now == nil {
		t.Fatal("expected Now to always be set")
	}
	if d.Now().IsZero() {
		t.Error("expected Now() to return the current time")
	}
}

func TestBuildCheckDepsDockerAlwaysOnPi(t *testing.T) {
	deps, _ := newTestDeps()
	cfg := config.Config{Node: config.NodePi}
	d, err := buildCheckDeps(context.Background(), deps, &GlobalFlags{}, cfg, config.Secrets{})
	if err != nil {
		t.Fatalf("buildCheckDeps: %v", err)
	}
	if d.Docker == nil {
		t.Error("expected Docker to always be built on the pi node")
	}
}

func TestBuildCheckDepsDockerOnNASRequiresExistingSocket(t *testing.T) {
	deps, _ := newTestDeps()

	missing := config.Config{Node: config.NodeNAS, Docker: config.Docker{Socket: filepath.Join(t.TempDir(), "no-such.sock")}}
	d, err := buildCheckDeps(context.Background(), deps, &GlobalFlags{}, missing, config.Secrets{})
	if err != nil {
		t.Fatalf("buildCheckDeps: %v", err)
	}
	if d.Docker != nil {
		t.Error("expected Docker to stay nil when the configured socket doesn't exist")
	}

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "docker.sock")
	if err := os.WriteFile(sockPath, nil, 0o644); err != nil {
		t.Fatalf("write fake socket file: %v", err)
	}
	present := config.Config{Node: config.NodeNAS, Docker: config.Docker{Socket: sockPath}}
	d, err = buildCheckDeps(context.Background(), deps, &GlobalFlags{}, present, config.Secrets{})
	if err != nil {
		t.Fatalf("buildCheckDeps: %v", err)
	}
	if d.Docker == nil {
		t.Error("expected Docker to be built when the configured socket exists")
	}
}

func TestBuildCheckDepsConstructorErrorPropagates(t *testing.T) {
	deps, _ := newTestDeps()
	boom := errors.New("boom")
	deps.Sonarr = func(config.Config, config.Secrets) (sonarr.Client, error) { return nil, boom }
	cfg := config.Config{Node: config.NodePi, Services: config.Services{Sonarr: config.Service{URL: "http://sonarr:8989"}}}

	if _, err := buildCheckDeps(context.Background(), deps, &GlobalFlags{}, cfg, config.Secrets{}); err == nil {
		t.Fatal("expected the failing Sonarr constructor's error to propagate")
	}
}

func TestBuildCheckDepsHostConstructorErrorPropagates(t *testing.T) {
	deps, _ := newTestDeps()
	boom := errors.New("boom")
	deps.Host = func(config.Config, config.Secrets) (hostfs.Client, error) { return nil, boom }
	if _, err := buildCheckDeps(context.Background(), deps, &GlobalFlags{}, config.Config{Node: config.NodePi}, config.Secrets{}); err == nil {
		t.Fatal("expected the failing Host constructor's error to propagate")
	}
}

func TestRegistryForFallsBackWhenUnset(t *testing.T) {
	deps := &Deps{} // Registry left nil
	reg, err := registryFor(deps, config.Config{})
	if err != nil {
		t.Fatalf("registryFor: %v", err)
	}
	if len(reg.All()) == 0 {
		t.Error("expected the real checks.Registry fallback to return a non-empty catalogue")
	}
}

func TestRegistryForWrapsError(t *testing.T) {
	deps, _ := newTestDeps()
	boom := errors.New("boom")
	deps.Registry = func(config.Config) (*check.Registry, error) { return nil, boom }
	if _, err := registryFor(deps, config.Config{}); err == nil {
		t.Fatal("expected registryFor to propagate the Registry function's error")
	}
}
