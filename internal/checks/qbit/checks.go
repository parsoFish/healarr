// Package qbit implements the qBittorrent check family: torrents stuck in
// an error or stalled state, completed torrents the *arrs never imported,
// disallowed file types (executables masquerading as episodes, stray ISOs
// in TV categories), and torrents that have seeded to their configured
// target and been imported, so they are safe to remove.
package qbit

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
		stalledErroredCheck(),
		completedNotImportedCheck(),
		wrongFileTypeCheck(),
		seededDoneCheck(),
	}
}
