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

// Checks returns this family's catalogue rows. cfg is read for thresholds
// only; clients and the live config come from check.Deps at run time, so
// every check in this family stays pure and testable without reconstructing
// the catalogue per test case.
func Checks(_ config.Config) []check.Check {
	return []check.Check{
		arrHealthCheck(),
		arrQueueStuckCheck(),
		arrWantedMissingSpikeCheck(),
		serviceUpdateAvailableCheck(),
	}
}
