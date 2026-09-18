// Package mounts implements the mount health check family: mount_race
// detects a container whose bind mount silently fell through to the
// container's own root filesystem (the 2026-09-12 incident), and
// host_mount_health watches that the host side of each configured mount is
// actually mounted and readable.
package mounts

import (
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// Checks returns this family's catalogue rows. The cfg parameter is
// unused: both checks read the mount list from check.Deps.Cfg at Run time
// rather than from this constructor, so the catalogue is independent of
// any particular config value and each check stays pure and
// table-testable.
func Checks(_ config.Config) []check.Check {
	return []check.Check{
		mountRaceCheck(),
		hostMountHealthCheck(),
	}
}
