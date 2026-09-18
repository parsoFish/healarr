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
	// PublicURL is the digest email's BaseURL (e.g.
	// "http://192.0.2.10/healarr"): the LAN address an operator's mail
	// client can actually reach, which need not equal ListenAddr (a bind
	// address such as "0.0.0.0:8091" isn't a URL a browser can open).
	// "" (the default) omits the digest's "Decisions:" link entirely.
	PublicURL string `toml:"public_url"`
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
	// PeerMessageRetention is how far back the nightly checkpoint job keeps
	// peer_messages rows; anything older is pruned (default 720h = 30 days).
	PeerMessageRetention time.Duration `toml:"peer_message_retention"`
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

// Staleness configures the "is this title stale" scorer: one point weight
// per row of the C6 table, plus the thresholds and per-title state
// (owner, snooze) the decision workflow reads alongside the score.
type Staleness struct {
	DaysMaxPoints         float64 `toml:"days_max_points"`          // 40
	DaysHorizon           int     `toml:"days_horizon"`             // 180
	NeverWatchedAfterDays int     `toml:"never_watched_after_days"` // 30 (never watched & added > this => full days points)
	WatchedFullPoints     float64 `toml:"watched_full_points"`      // +15
	WatchedPartialPoints  float64 `toml:"watched_partial_points"`   // -10
	EndedPoints           float64 `toml:"ended_points"`             // +10
	ContinuingPoints      float64 `toml:"continuing_points"`        // -15
	SizePointsPerGB       float64 `toml:"size_points_per_gb"`       // 0.1 (GB/10)
	SizeMaxPoints         float64 `toml:"size_max_points"`          // 15
	OtherRequesterPoints  float64 `toml:"other_requester_points"`   // +10
	OwnerRequesterPoints  float64 `toml:"owner_requester_points"`   // -5
	AgePointsPerDay       float64 `toml:"age_points_per_day"`       // 0.05
	AgeMaxPoints          float64 `toml:"age_max_points"`           // 10
	CandidateThreshold    float64 `toml:"candidate_threshold"`      // 70
	WatchlistThreshold    float64 `toml:"watchlist_threshold"`      // 50
	Owner                 string  `toml:"owner"`                    // Overseerr username/email of the owner ("" = unknown)
	SnoozeDays            int     `toml:"snooze_days"`              // 60
}

// Cleanup configures the disk-cleanup executor's safety rails: how
// cautious it is (dry_run) and how old something must be before it is
// eligible for removal.
type Cleanup struct {
	DryRun         bool          `toml:"dry_run"`         // true
	OrphanMinAge   time.Duration `toml:"orphan_min_age"`  // 168h
	RecycleMinAge  time.Duration `toml:"recycle_min_age"` // 24h
	DockerDangling bool          `toml:"docker_dangling"` // true (prune dangling only)
}

// Actions gates every mutating path (decision.Execute's delete branch,
// cleanup.Execute, the NAS-side qbit_delete) behind an explicit opt-in.
// Keep (snooze) is not a stack mutation and is always allowed. Default
// false: it must stay false until the operator trusts the actions.
type Actions struct {
	Enabled bool `toml:"enabled"` // false
}

// Config is the full non-secret configuration for one node.
type Config struct {
	Node      Node      `toml:"node"`
	Services  Services  `toml:"services"`
	Peer      Peer      `toml:"peer"`
	Web       Web       `toml:"web"`
	Email     Email     `toml:"email"`
	LLM       LLM       `toml:"llm"`
	State     State     `toml:"state"`
	Docker    Docker    `toml:"docker"`
	Mounts    []Mount   `toml:"mounts"`
	Checks    Checks    `toml:"checks"`
	Agent     Agent     `toml:"agent"`
	Staleness Staleness `toml:"staleness"`
	Cleanup   Cleanup   `toml:"cleanup"`
	Actions   Actions   `toml:"actions"`
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
