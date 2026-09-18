package check

import (
	"time"

	"github.com/parsoFish/healarr/internal/clients/docker"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
	"github.com/parsoFish/healarr/internal/clients/plex"
	"github.com/parsoFish/healarr/internal/clients/prowlarr"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/clients/tautulli"
	"github.com/parsoFish/healarr/internal/config"
)

// Deps is everything a check may read. Any client may be nil when the
// service is not configured on this node; a check that needs a nil client
// returns ErrNotConfigured.
type Deps struct {
	Node     config.Node
	Cfg      config.Config
	Now      func() time.Time
	Previous *Report // last persisted report for this node; nil on first run or --dry-run without store

	Sonarr    sonarr.Client
	Radarr    radarr.Client
	Prowlarr  prowlarr.Client
	QBit      qbittorrent.Client
	Plex      plex.Client
	Tautulli  tautulli.Client
	Overseerr overseerr.Client
	Docker    docker.Client
	Host      hostfs.Client
}

// PreviousMetric returns the named metric from the previous report.
func (d Deps) PreviousMetric(name string) (float64, bool) {
	if d.Previous == nil || d.Previous.Metrics == nil {
		return 0, false
	}
	v, ok := d.Previous.Metrics[name]
	return v, ok
}

// NewFinding builds a Finding stamped with d.Node and d.Now() on both
// FirstSeen and LastSeen (the store adjusts FirstSeen on upsert).
func (d Deps) NewFinding(checkID, entityKey string, sev Severity, tier Tier, summary string) Finding {
	now := d.Now()
	return Finding{CheckID: checkID, Node: d.Node, EntityKey: entityKey, Severity: sev, Tier: tier, Summary: summary, FirstSeen: now, LastSeen: now}
}
