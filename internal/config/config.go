// Package config loads healarr's TOML configuration and secrets.
package config

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
