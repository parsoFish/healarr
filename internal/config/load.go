package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const secretsEnv = "HEALARR_SECRETS"

// Load reads config + secrets, applies env overrides, resolves *arr keys.
func Load(configPath string) (Config, Secrets, error) {
	cfg := Defaults()
	if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
		return Config{}, Secrets{}, fmt.Errorf("read config %s: %w", configPath, err)
	}
	secretsPath := os.Getenv(secretsEnv)
	if secretsPath == "" {
		secretsPath = filepath.Join(filepath.Dir(configPath), "secrets.toml")
	}
	sec, err := loadSecrets(secretsPath)
	if err != nil {
		return Config{}, Secrets{}, err
	}
	cfg = applyConfigEnv(cfg)
	sec = applySecretsEnv(sec)
	sec, err = resolveArrKeys(cfg, sec)
	if err != nil {
		return Config{}, Secrets{}, err
	}
	if err := validate(cfg); err != nil {
		return Config{}, Secrets{}, err
	}
	return cfg, sec, nil
}

func loadSecrets(path string) (Secrets, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Secrets{}, fmt.Errorf("secrets file: %w", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return Secrets{}, fmt.Errorf("secrets file %s has mode %o; must be 0600", path, perm)
	}
	var sec Secrets
	if _, err := toml.DecodeFile(path, &sec); err != nil {
		return Secrets{}, fmt.Errorf("read secrets %s: %w", path, err)
	}
	return sec, nil
}

func envOr(key, current string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return current
}

func applyConfigEnv(in Config) Config {
	out := in
	out.Services.Sonarr.URL = envOr("HEALARR_SONARR_URL", in.Services.Sonarr.URL)
	out.Services.Radarr.URL = envOr("HEALARR_RADARR_URL", in.Services.Radarr.URL)
	out.Services.Prowlarr.URL = envOr("HEALARR_PROWLARR_URL", in.Services.Prowlarr.URL)
	out.Services.Overseerr.URL = envOr("HEALARR_OVERSEERR_URL", in.Services.Overseerr.URL)
	out.Services.QBittorrent.URL = envOr("HEALARR_QBITTORRENT_URL", in.Services.QBittorrent.URL)
	out.Services.Plex.URL = envOr("HEALARR_PLEX_URL", in.Services.Plex.URL)
	out.Services.Tautulli.URL = envOr("HEALARR_TAUTULLI_URL", in.Services.Tautulli.URL)
	out.State.DBPath = envOr("HEALARR_STATE_DB", in.State.DBPath)
	out.Peer.PeerURL = envOr("HEALARR_PEER_URL", in.Peer.PeerURL)
	return out
}

func applySecretsEnv(in Secrets) Secrets {
	out := in
	out.SonarrAPIKey = envOr("HEALARR_SONARR_API_KEY", in.SonarrAPIKey)
	out.RadarrAPIKey = envOr("HEALARR_RADARR_API_KEY", in.RadarrAPIKey)
	out.ProwlarrAPIKey = envOr("HEALARR_PROWLARR_API_KEY", in.ProwlarrAPIKey)
	out.OverseerrAPIKey = envOr("HEALARR_OVERSEERR_API_KEY", in.OverseerrAPIKey)
	out.QBitUser = envOr("HEALARR_QBIT_USER", in.QBitUser)
	out.QBitPass = envOr("HEALARR_QBIT_PASS", in.QBitPass)
	out.PlexToken = envOr("HEALARR_PLEX_TOKEN", in.PlexToken)
	out.TautulliAPIKey = envOr("HEALARR_TAUTULLI_API_KEY", in.TautulliAPIKey)
	out.PeerToken = envOr("HEALARR_PEER_TOKEN", in.PeerToken)
	out.WebToken = envOr("HEALARR_WEB_TOKEN", in.WebToken)
	out.AnthropicAPIKey = envOr("ANTHROPIC_API_KEY", in.AnthropicAPIKey)
	return out
}

// resolveArrKeys fills empty *arr keys from their config.xml when a path is configured.
func resolveArrKeys(cfg Config, in Secrets) (Secrets, error) {
	out := in
	type slot struct {
		file string
		key  *string
		name string
	}
	slots := []slot{
		{cfg.Services.Sonarr.APIKeyFile, &out.SonarrAPIKey, "sonarr"},
		{cfg.Services.Radarr.APIKeyFile, &out.RadarrAPIKey, "radarr"},
		{cfg.Services.Prowlarr.APIKeyFile, &out.ProwlarrAPIKey, "prowlarr"},
	}
	for _, s := range slots {
		if *s.key != "" || s.file == "" {
			continue
		}
		k, err := ReadArrAPIKey(s.file)
		if err != nil {
			return Secrets{}, fmt.Errorf("%s api key: %w", s.name, err)
		}
		*s.key = k
	}
	return out, nil
}

func validate(cfg Config) error {
	switch cfg.Node {
	case NodePi, NodeNAS:
		return nil
	case "":
		return errors.New("config: node is required (\"pi\" or \"nas\")")
	default:
		return fmt.Errorf("config: unknown node %q (want \"pi\" or \"nas\")", cfg.Node)
	}
}
