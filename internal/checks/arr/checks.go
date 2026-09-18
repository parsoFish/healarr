// Package arr implements the *arr family checks: arr_health watches
// Sonarr/Radarr/Prowlarr health items, arr_queue_stuck watches their
// download queues for stalled or failed items, arr_wanted_missing_spike
// watches for a sudden jump in wanted/missing counts, and
// service_update_available surfaces pending application updates.
package arr

import (
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// Checks returns this family's catalogue rows. The cfg parameter is
// unused: every check here reads its thresholds (QueueStuckAfter,
// WantedSpikePercent, WantedSpikeMin) from check.Deps.Cfg.Checks at Run
// time rather than from this constructor, so the catalogue is independent
// of any particular config value and each check stays pure and
// table-testable.
func Checks(_ config.Config) []check.Check {
	return []check.Check{
		arrHealthCheck(),
		arrQueueStuckCheck(),
		arrWantedMissingSpikeCheck(),
		serviceUpdateAvailableCheck(),
	}
}
