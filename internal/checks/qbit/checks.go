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

// Checks returns this family's catalogue rows. The cfg parameter is
// unused: every check here reads its thresholds (QBitStalledAfter,
// WrongFileExts, SeededMinAge, ...) from check.Deps.Cfg.Checks at Run time
// rather than from this constructor, so the catalogue is independent of
// any particular config value and each check stays pure and
// table-testable.
func Checks(_ config.Config) []check.Check {
	return []check.Check{
		stalledErroredCheck(),
		completedNotImportedCheck(),
		wrongFileTypeCheck(),
		seededDoneCheck(),
	}
}
