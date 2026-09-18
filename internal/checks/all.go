// Package checks aggregates every check family into one registry: the
// catalogue the Phase 3 daemon and the CLI's --dry-run schedule against.
package checks

import (
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/checks/arr"
	"github.com/parsoFish/healarr/internal/checks/disk"
	"github.com/parsoFish/healarr/internal/checks/indexers"
	"github.com/parsoFish/healarr/internal/checks/mounts"
	"github.com/parsoFish/healarr/internal/checks/plex"
	"github.com/parsoFish/healarr/internal/checks/qbit"
	"github.com/parsoFish/healarr/internal/checks/requests"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/staleness"
)

// Registry builds the full catalogue for cfg. It returns an error if two
// families register the same id.
func Registry(cfg config.Config) (*check.Registry, error) {
	r := check.NewRegistry()
	families := [][]check.Check{
		mounts.Checks(cfg),
		arr.Checks(cfg),
		indexers.Checks(cfg),
		requests.Checks(cfg),
		qbit.Checks(cfg),
		disk.Checks(cfg),
		plex.Checks(cfg),
		staleness.Checks(cfg),
	}
	for _, family := range families {
		for _, c := range family {
			if err := r.Register(c); err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}
