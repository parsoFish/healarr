// Package indexers implements the indexer health check family:
// indexer_failures watches Prowlarr's indexers for ones Prowlarr has
// disabled after repeated failures, or that failed recently but are still
// enabled.
package indexers

import (
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// Checks returns this family's catalogue rows. The cfg parameter is
// unused: indexer_failures reads its threshold (IndexerFailureWindow) from
// check.Deps.Cfg.Checks at Run time rather than from this constructor, so
// the catalogue is independent of any particular config value and the
// check itself stays pure and table-testable.
func Checks(_ config.Config) []check.Check {
	return []check.Check{
		indexerFailuresCheck(),
	}
}
