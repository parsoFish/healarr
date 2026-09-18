// Package config loads healarr's TOML configuration and secrets.
package config

import (
	"fmt"
	"time"
)

// Node identifies which host this agent runs on.
type Node string

const (
	NodePi  Node = "pi"
	NodeNAS Node = "nas"
)

// Service is one upstream HTTP service.
type Service struct {
	URL        string `toml:"url"`
	APIKeyFile string `toml:"api_key_file"` // optional: path to *arr config.xml
}

// Services groups every upstream.
type Services struct {
	Sonarr      Service `toml:"sonarr"`
	Radarr      Service `toml:"radarr"`
	Prowlarr    Service `toml:"prowlarr"`
	Overseerr   Service `toml:"overseerr"`
	QBittorrent Service `toml:"qbittorrent"`
	Plex        Service `toml:"plex"`
	Tautulli    Service `toml:"tautulli"`
}

// Peer configures the node-to-node HTTP channel.
type Peer struct {
	ListenAddr string `toml:"listen_addr"`
	PeerURL    string `toml:"peer_url"`
}

// Web configures the LAN web UI (Pi only).
type Web struct {
	ListenAddr string `toml:"listen_addr"`
	BasePath   string `toml:"base_path"`
}

// Email configures outbound mail via msmtp.
type Email struct {
	To        string `toml:"to"`
	From      string `toml:"from"`
	MsmtpPath string `toml:"msmtp_path"`
	DigestAt  string `toml:"digest_at"` // "HH:MM" local, default "07:00"
}

// Agent configures the daemon's schedules.
type Agent struct {
	Timezone          string        `toml:"timezone"`           // IANA name; "" = time.Local
	HeartbeatInterval time.Duration `toml:"heartbeat_interval"` // default 5m
	CheckpointAt      string        `toml:"checkpoint_at"`      // "HH:MM" local, default "03:00"
	PeerStaleAfter    time.Duration `toml:"peer_stale_after"`   // default 15m (3 missed heartbeats)
}

// LLM configures the daily digest model call.
type LLM struct {
	Enabled        bool    `toml:"enabled"`
	Model          string  `toml:"model"`
	DailyBudgetUSD float64 `toml:"daily_budget_usd"`
}

// State configures on-disk state.
type State struct {
	DBPath string `toml:"db_path"`
}

// Docker configures access to the local Docker engine.
type Docker struct {
	Socket string `toml:"socket"`
	Binary string `toml:"binary"`
}

// Mount pairs a host mount with the container path that should see it.
type Mount struct {
	Host          string `toml:"host"`
	Container     string `toml:"container"`
	ContainerPath string `toml:"container_path"`
}

// PlexLibrary maps a Plex library title to the host directory its files live in.
type PlexLibrary struct {
	Title string `toml:"title"`
	Path  string `toml:"path"`
}

// Checks holds every threshold the check catalogue reads. Durations are
// TOML strings ("24h", "30m"); BurntSushi/toml decodes them.
type Checks struct {
	Timeout                   time.Duration `toml:"timeout"`    // per-check timeout
	DiskPaths                 []string      `toml:"disk_paths"` // filesystems to watch for pressure
	DiskWarnPercent           float64       `toml:"disk_warn_percent"`
	DiskCritPercent           float64       `toml:"disk_crit_percent"`
	QueueStuckAfter           time.Duration `toml:"queue_stuck_after"`
	QBitStalledAfter          time.Duration `toml:"qbit_stalled_after"`
	CompletedNotImportedAfter time.Duration `toml:"completed_not_imported_after"`
	ArrHistoryWindow          time.Duration `toml:"arr_history_window"`
	WrongFileExts             []string      `toml:"wrong_file_exts"`
	TVCategories              []string      `toml:"tv_categories"` // qBit categories where .iso is also wrong
	DownloadsDirs             []string      `toml:"downloads_dirs"`
	OrphanAfter               time.Duration `toml:"orphan_after"`
	RecycleDirs               []string      `toml:"recycle_dirs"`
	RecycleWarnGB             float64       `toml:"recycle_warn_gb"`
	ImageBloatWarnGB          float64       `toml:"image_bloat_warn_gb"`
	LogDirs                   []string      `toml:"log_dirs"`
	LogWarnGB                 float64       `toml:"log_warn_gb"`
	WantedSpikePercent        float64       `toml:"wanted_spike_percent"`
	WantedSpikeMin            int           `toml:"wanted_spike_min"`
	IndexerFailureWindow      time.Duration `toml:"indexer_failure_window"`
	OverseerrStuckAfter       time.Duration `toml:"overseerr_stuck_after"`
	PlexScanStaleAfter        time.Duration `toml:"plex_scan_stale_after"`
	PlexLibraries             []PlexLibrary `toml:"plex_libraries"`
	SeededMinAge              time.Duration `toml:"seeded_min_age"`
}

// Config is the full non-secret configuration for one node.
type Config struct {
	Node     Node     `toml:"node"`
	Services Services `toml:"services"`
	Peer     Peer     `toml:"peer"`
	Web      Web      `toml:"web"`
	Email    Email    `toml:"email"`
	LLM      LLM      `toml:"llm"`
	State    State    `toml:"state"`
	Docker   Docker   `toml:"docker"`
	Mounts   []Mount  `toml:"mounts"`
	Checks   Checks   `toml:"checks"`
	Agent    Agent    `toml:"agent"`
}

// Location resolves the timezone the daemon's schedules run in. An empty
// Agent.Timezone defaults to the host's local zone, matching the doc
// comment on Agent.Timezone; validate() has already proven a non-empty
// value loads cleanly, but Location fails closed on its own rather than
// trusting that call site.
func (c Config) Location() (*time.Location, error) {
	if c.Agent.Timezone == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(c.Agent.Timezone)
	if err != nil {
		return nil, fmt.Errorf("config: agent.timezone %q: %w", c.Agent.Timezone, err)
	}
	return loc, nil
}

// Secrets is everything that must never be logged or committed.
type Secrets struct {
	SonarrAPIKey    string `toml:"sonarr_api_key"`
	RadarrAPIKey    string `toml:"radarr_api_key"`
	ProwlarrAPIKey  string `toml:"prowlarr_api_key"`
	OverseerrAPIKey string `toml:"overseerr_api_key"`
	QBitUser        string `toml:"qbit_user"`
	QBitPass        string `toml:"qbit_pass"`
	PlexToken       string `toml:"plex_token"`
	TautulliAPIKey  string `toml:"tautulli_api_key"`
	PeerToken       string `toml:"peer_token"`
	WebToken        string `toml:"web_token"`
	AnthropicAPIKey string `toml:"anthropic_api_key"`
}
