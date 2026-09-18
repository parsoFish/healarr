package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/checks"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
	"github.com/parsoFish/healarr/internal/clients/plex"
	"github.com/parsoFish/healarr/internal/clients/prowlarr"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/clients/tautulli"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// StoreAPI is the subset of *store.Store the check/report verbs need: save
// a fresh report, read the latest one back for delta checks and digest
// counts, list open findings for the digest, and close the connection. A
// small interface (rather than *store.Store directly) lets tests swap in a
// FakeStore; the compile-time assertion below keeps it honest against the
// real store package.
type StoreAPI interface {
	SaveReport(ctx context.Context, rep check.Report) (int64, store.UpsertSummary, error)
	LatestReport(ctx context.Context, node config.Node) (check.Report, bool, error)
	OpenFindings(ctx context.Context, node config.Node) ([]store.StoredFinding, error)
	Close() error
}

var _ StoreAPI = (*store.Store)(nil)

// registryFor builds the check catalogue for cfg via deps.Registry, falling
// back to the real checks.Registry when unset (mirroring how (d *Deps)
// load falls back to config.Load), so a bare &Deps{} in a test still works.
func registryFor(deps *Deps, cfg config.Config) (*check.Registry, error) {
	regFn := deps.Registry
	if regFn == nil {
		regFn = checks.Registry
	}
	reg, err := regFn(cfg)
	if err != nil {
		return nil, fmt.Errorf("check registry: %w", err)
	}
	return reg, nil
}

// openStore opens cfg's state store via deps.OpenStore, falling back to
// the real store when unset (mirroring registryFor and (d *Deps) load), so
// a bare &Deps{} in a test reaches the same code path production does
// rather than panicking on a nil function.
func openStore(ctx context.Context, deps *Deps, cfg config.Config) (StoreAPI, error) {
	openFn := deps.OpenStore
	if openFn == nil {
		openFn = defaultOpenStore
	}
	return openFn(ctx, cfg)
}

// defaultOpenStore opens the node's sqlite store at cfg.State.DBPath.
func defaultOpenStore(ctx context.Context, cfg config.Config) (StoreAPI, error) {
	st, err := store.Open(ctx, cfg.State.DBPath)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// buildCheckDeps constructs check.Deps for cfg.Node. Each service client is
// built via deps.<Svc> only when its URL is configured, so an unconfigured
// service stays nil and the relevant checks see check.ErrNotConfigured
// rather than a broken client. Docker is built unconditionally on the pi
// node and, on nas, only when cfg.Docker.Socket exists as a file; Host is
// always built. Now is always time.Now, never nil (check.Deps.Now is
// called unconditionally by the runner). A constructor's own error is
// returned, never swallowed.
func buildCheckDeps(_ context.Context, deps *Deps, _ *GlobalFlags, cfg config.Config, sec config.Secrets) (check.Deps, error) {
	d := check.Deps{Node: cfg.Node, Cfg: cfg, Now: time.Now}

	if err := setConfiguredClients(&d, deps, cfg, sec); err != nil {
		return check.Deps{}, err
	}
	if err := setDockerAndHost(&d, deps, cfg, sec); err != nil {
		return check.Deps{}, err
	}
	return d, nil
}

// setConfiguredClients builds every URL-gated service client onto d.
func setConfiguredClients(d *check.Deps, deps *Deps, cfg config.Config, sec config.Secrets) error {
	if err := setClient(cfg.Services.Sonarr.URL, &d.Sonarr, func() (sonarr.Client, error) { return deps.Sonarr(cfg, sec) }); err != nil {
		return fmt.Errorf("check deps: sonarr: %w", err)
	}
	if err := setClient(cfg.Services.Radarr.URL, &d.Radarr, func() (radarr.Client, error) { return deps.Radarr(cfg, sec) }); err != nil {
		return fmt.Errorf("check deps: radarr: %w", err)
	}
	if err := setClient(cfg.Services.Prowlarr.URL, &d.Prowlarr, func() (prowlarr.Client, error) { return deps.Prowlarr(cfg, sec) }); err != nil {
		return fmt.Errorf("check deps: prowlarr: %w", err)
	}
	if err := setClient(cfg.Services.QBittorrent.URL, &d.QBit, func() (qbittorrent.Client, error) { return deps.QBit(cfg, sec) }); err != nil {
		return fmt.Errorf("check deps: qbittorrent: %w", err)
	}
	if err := setClient(cfg.Services.Plex.URL, &d.Plex, func() (plex.Client, error) { return deps.Plex(cfg, sec) }); err != nil {
		return fmt.Errorf("check deps: plex: %w", err)
	}
	if err := setClient(cfg.Services.Tautulli.URL, &d.Tautulli, func() (tautulli.Client, error) { return deps.Tautulli(cfg, sec) }); err != nil {
		return fmt.Errorf("check deps: tautulli: %w", err)
	}
	if err := setClient(cfg.Services.Overseerr.URL, &d.Overseerr, func() (overseerr.Client, error) { return deps.Overseerr(cfg, sec) }); err != nil {
		return fmt.Errorf("check deps: overseerr: %w", err)
	}
	return nil
}

// setClient builds *target via build only when url is non-empty, leaving
// *target at its zero value (nil, for every client interface here)
// otherwise.
func setClient[C any](url string, target *C, build func() (C, error)) error {
	if url == "" {
		return nil
	}
	c, err := build()
	if err != nil {
		return err
	}
	*target = c
	return nil
}

// setDockerAndHost builds d.Docker (per wantDocker's node/socket rule) and
// d.Host (always).
func setDockerAndHost(d *check.Deps, deps *Deps, cfg config.Config, sec config.Secrets) error {
	if wantDocker(cfg) {
		dk, err := deps.Docker(cfg, sec)
		if err != nil {
			return fmt.Errorf("check deps: docker: %w", err)
		}
		d.Docker = dk
	}
	h, err := deps.Host(cfg, sec)
	if err != nil {
		return fmt.Errorf("check deps: host: %w", err)
	}
	d.Host = h
	return nil
}

// wantDocker decides whether buildCheckDeps should construct a Docker
// client: always on the pi node, and on nas only when cfg.Docker.Socket
// exists as a file (the NAS doesn't always run a Docker engine).
func wantDocker(cfg config.Config) bool {
	switch cfg.Node {
	case config.NodePi:
		return true
	case config.NodeNAS:
		if cfg.Docker.Socket == "" {
			return false
		}
		_, err := os.Stat(cfg.Docker.Socket)
		return err == nil
	default:
		return false
	}
}
