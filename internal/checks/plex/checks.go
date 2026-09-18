// Package plex implements the Plex check family: plex_reachability watches
// that Plex answers /identity, and plex_scan_freshness watches that each
// configured library has been rescanned recently enough to reflect the
// newest file sitting in its host directory.
package plex

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
		plexReachabilityCheck(),
		plexScanFreshnessCheck(),
	}
}
