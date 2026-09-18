// Package plex implements the Plex check family: plex_reachability watches
// that Plex answers /identity, and plex_scan_freshness watches that each
// configured library has been rescanned recently enough to reflect the
// newest file sitting in its host directory.
package plex

import (
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// Checks returns this family's catalogue rows. The cfg parameter is
// unused: plex_scan_freshness reads its threshold and library list
// (PlexScanStaleAfter, PlexLibraries) from check.Deps.Cfg.Checks at Run
// time rather than from this constructor, so the catalogue is independent
// of any particular config value and each check stays pure and
// table-testable.
func Checks(_ config.Config) []check.Check {
	return []check.Check{
		plexReachabilityCheck(),
		plexScanFreshnessCheck(),
	}
}
