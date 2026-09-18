// Package requests implements the request-pipeline check family:
// tautulli_reachability watches that Tautulli answers pings and reports
// its active-stream count, and overseerr_stuck_processing watches
// Overseerr requests stuck in the "processing" state.
package requests

import (
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// Checks returns this family's catalogue rows. The cfg parameter is
// unused: overseerr_stuck_processing reads its threshold
// (OverseerrStuckAfter) from check.Deps.Cfg.Checks at Run time rather than
// from this constructor, so the catalogue is independent of any
// particular config value and each check stays pure and table-testable.
func Checks(_ config.Config) []check.Check {
	return []check.Check{
		tautulliReachabilityCheck(),
		overseerrStuckProcessingCheck(),
	}
}
