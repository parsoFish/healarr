package cli

import (
	"fmt"
	"net"
	"net/url"

	"github.com/parsoFish/healarr/internal/clients/docker"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/httpx"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
	"github.com/parsoFish/healarr/internal/clients/plex"
	"github.com/parsoFish/healarr/internal/clients/prowlarr"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/clients/tautulli"
	"github.com/parsoFish/healarr/internal/config"
)

// Deps carries every external dependency the CLI verbs need: a config
// loader plus one constructor per service client. Commands build their
// client lazily inside RunE via load, so a config-free command such as
// `version` never touches disk. Tests set Load and the constructors to
// return each package's Fake.
type Deps struct {
	Load      func(path string) (config.Config, config.Secrets, error)
	Sonarr    func(cfg config.Config, sec config.Secrets) (sonarr.Client, error)
	Radarr    func(cfg config.Config, sec config.Secrets) (radarr.Client, error)
	Prowlarr  func(cfg config.Config, sec config.Secrets) (prowlarr.Client, error)
	QBit      func(cfg config.Config, sec config.Secrets) (qbittorrent.Client, error)
	Plex      func(cfg config.Config, sec config.Secrets) (plex.Client, error)
	Tautulli  func(cfg config.Config, sec config.Secrets) (tautulli.Client, error)
	Overseerr func(cfg config.Config, sec config.Secrets) (overseerr.Client, error)
	Docker    func(cfg config.Config, sec config.Secrets) (docker.Client, error)
	Host      func(cfg config.Config, sec config.Secrets) (hostfs.Client, error)
}

// DefaultDeps wires the real constructors, reading service URLs and
// secrets from config.Load at call time.
func DefaultDeps() *Deps {
	return &Deps{
		Load: config.Load,
		Sonarr: func(cfg config.Config, sec config.Secrets) (sonarr.Client, error) {
			return sonarr.New(cfg.Services.Sonarr.URL, sec.SonarrAPIKey)
		},
		Radarr: func(cfg config.Config, sec config.Secrets) (radarr.Client, error) {
			return radarr.New(cfg.Services.Radarr.URL, sec.RadarrAPIKey)
		},
		Prowlarr: func(cfg config.Config, sec config.Secrets) (prowlarr.Client, error) {
			return prowlarr.New(cfg.Services.Prowlarr.URL, sec.ProwlarrAPIKey)
		},
		QBit: func(cfg config.Config, sec config.Secrets) (qbittorrent.Client, error) {
			return qbittorrent.New(cfg.Services.QBittorrent.URL, sec.QBitUser, sec.QBitPass)
		},
		Plex: func(cfg config.Config, sec config.Secrets) (plex.Client, error) {
			return plex.New(cfg.Services.Plex.URL, sec.PlexToken, plexTLSOpts(cfg.Services.Plex.URL)...)
		},
		Tautulli: func(cfg config.Config, sec config.Secrets) (tautulli.Client, error) {
			return tautulli.New(cfg.Services.Tautulli.URL, sec.TautulliAPIKey)
		},
		Overseerr: func(cfg config.Config, sec config.Secrets) (overseerr.Client, error) {
			return overseerr.New(cfg.Services.Overseerr.URL, sec.OverseerrAPIKey)
		},
		Docker: func(cfg config.Config, _ config.Secrets) (docker.Client, error) {
			return docker.New(cfg.Docker.Socket, cfg.Docker.Binary)
		},
		Host: func(config.Config, config.Secrets) (hostfs.Client, error) {
			return hostfs.New(""), nil
		},
	}
}

// plexTLSOpts derives a WithTLSServerName option from rawURL's hostname so
// Plex's plex.direct certificate (issued for a dashed-IP hostname, not the
// bare IP it resolves to) verifies. It returns nil when rawURL doesn't
// parse or its host is a literal IP, since plex.direct SNI doesn't apply
// there.
func plexTLSOpts(rawURL string) []httpx.Option {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return nil
	}
	return []httpx.Option{httpx.WithTLSServerName(host)}
}

// load reads config + secrets for the current invocation, using d.Load
// (config.Load by default) and naming the config path on failure.
func (d *Deps) load(flags *GlobalFlags) (config.Config, config.Secrets, error) {
	loadFn := d.Load
	if loadFn == nil {
		loadFn = config.Load
	}
	cfg, sec, err := loadFn(flags.ConfigPath)
	if err != nil {
		return config.Config{}, config.Secrets{}, fmt.Errorf("load config %s: %w", flags.ConfigPath, err)
	}
	return cfg, sec, nil
}

// newGetter builds a lazy client factory for one service: it loads config
// via deps.load only when called (never at registration time, so a
// config-free command such as `version` never touches disk), then calls
// ctor (one of deps.Sonarr, deps.Radarr, ...), wrapping a constructor
// failure with name for a clearer error.
func newGetter[C any](deps *Deps, flags *GlobalFlags, name string, ctor func(config.Config, config.Secrets) (C, error)) func() (C, error) {
	return func() (C, error) {
		var zero C
		cfg, sec, err := deps.load(flags)
		if err != nil {
			return zero, err
		}
		c, err := ctor(cfg, sec)
		if err != nil {
			return zero, fmt.Errorf("%s client: %w", name, err)
		}
		return c, nil
	}
}
