package cleanup

import (
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/docker"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

// fixedNow is the deterministic clock every test in this package uses.
var fixedNow = time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)

// depsOpt mutates a base check.Deps, letting each test set only the
// fields it cares about.
type depsOpt func(*check.Deps)

// baseDeps builds check.Deps for cfg on the NAS node with a fixed clock;
// opts (withHost, withDocker, withQBit, withSonarr) fill in whichever
// clients a test needs, leaving the rest nil (mirroring an unconfigured
// service).
func baseDeps(cfg config.Config, opts ...depsOpt) check.Deps {
	d := check.Deps{Node: config.NodeNAS, Cfg: cfg, Now: func() time.Time { return fixedNow }}
	for _, opt := range opts {
		opt(&d)
	}
	return d
}

func withHost(h hostfs.Client) depsOpt   { return func(d *check.Deps) { d.Host = h } }
func withDocker(c docker.Client) depsOpt { return func(d *check.Deps) { d.Docker = c } }
func withQBit(c qbittorrent.Client) depsOpt {
	return func(d *check.Deps) { d.QBit = c }
}
func withSonarr(c sonarr.Client) depsOpt { return func(d *check.Deps) { d.Sonarr = c } }

// enabledCfg returns a Config with the actions gate fully open (Actions.
// Enabled true, Cleanup.DryRun false) plus whatever cleanup/checks fields
// the caller overlays, so Execute tests exercise the real client call
// rather than short-circuiting on the gate.
func enabledCfg(over func(*config.Config)) config.Config {
	cfg := config.Config{
		Actions: config.Actions{Enabled: true},
		Cleanup: config.Cleanup{DryRun: false, OrphanMinAge: 168 * time.Hour, RecycleMinAge: 24 * time.Hour, DockerDangling: true},
	}
	if over != nil {
		over(&cfg)
	}
	return cfg
}
