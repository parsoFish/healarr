package cli

import (
	"testing"

	"github.com/parsoFish/healarr/internal/config"
)

func TestDefaultDepsConstructsRealClients(t *testing.T) {
	deps := DefaultDeps()
	cfg := config.Config{
		Services: config.Services{
			Sonarr:      config.Service{URL: "http://sonarr:8989"},
			Radarr:      config.Service{URL: "http://radarr:7878"},
			Prowlarr:    config.Service{URL: "http://prowlarr:9696"},
			Overseerr:   config.Service{URL: "http://overseerr:5055"},
			QBittorrent: config.Service{URL: "http://qbit:8080"},
			Plex:        config.Service{URL: "https://1-2-3-4.abcdef0123456789abcdef0123456789.plex.direct:32400"},
			Tautulli:    config.Service{URL: "http://tautulli:8181"},
		},
		Docker: config.Docker{Socket: "/var/run/docker.sock", Binary: "docker"},
	}
	sec := config.Secrets{
		SonarrAPIKey: "a", RadarrAPIKey: "b", ProwlarrAPIKey: "c", OverseerrAPIKey: "d",
		QBitUser: "u", QBitPass: "p", PlexToken: "t", TautulliAPIKey: "e",
	}

	checks := map[string]error{}
	if _, err := deps.Sonarr(cfg, sec); err != nil {
		checks["Sonarr"] = err
	}
	if _, err := deps.Radarr(cfg, sec); err != nil {
		checks["Radarr"] = err
	}
	if _, err := deps.Prowlarr(cfg, sec); err != nil {
		checks["Prowlarr"] = err
	}
	if _, err := deps.Overseerr(cfg, sec); err != nil {
		checks["Overseerr"] = err
	}
	if _, err := deps.QBit(cfg, sec); err != nil {
		checks["QBit"] = err
	}
	if _, err := deps.Plex(cfg, sec); err != nil {
		checks["Plex"] = err
	}
	if _, err := deps.Tautulli(cfg, sec); err != nil {
		checks["Tautulli"] = err
	}
	if _, err := deps.Docker(cfg, sec); err != nil {
		checks["Docker"] = err
	}
	if _, err := deps.Host(cfg, sec); err != nil {
		checks["Host"] = err
	}
	for name, err := range checks {
		t.Errorf("%s constructor: %v", name, err)
	}
}

func TestDefaultDepsDockerRejectsEmptyBinary(t *testing.T) {
	deps := DefaultDeps()
	cfg := config.Config{Docker: config.Docker{Socket: "/var/run/docker.sock"}}
	if _, err := deps.Docker(cfg, config.Secrets{}); err == nil {
		t.Fatal("expected error for empty docker binary")
	}
}

func TestPlexTLSOptsSkipsInvalidURLAndIPHost(t *testing.T) {
	if opts := plexTLSOpts("://bad-url"); opts != nil {
		t.Errorf("expected nil opts for invalid URL, got %v", opts)
	}
	if opts := plexTLSOpts("http://192.0.2.5:32400"); opts != nil {
		t.Errorf("expected nil opts for IP host, got %v", opts)
	}
}

func TestPlexTLSOptsSetsServerNameForHostname(t *testing.T) {
	opts := plexTLSOpts("https://1-2-3-4.abcdef0123456789abcdef0123456789.plex.direct:32400")
	if len(opts) != 1 {
		t.Fatalf("expected one TLS option for a hostname URL, got %d", len(opts))
	}
}

func TestDepsLoadDefaultsToConfigLoad(t *testing.T) {
	d := &Deps{}
	flags := &GlobalFlags{ConfigPath: "/no/such/config.toml"}
	if _, _, err := d.load(flags); err == nil {
		t.Fatal("expected error loading a nonexistent config path")
	}
}
