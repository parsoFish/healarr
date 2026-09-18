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

// Checks returns this family's catalogue rows. cfg is read for thresholds
// only; clients and the live config come from check.Deps at run time, so
// every check in this family stays pure and testable without reconstructing
// the catalogue per test case.
func Checks(_ config.Config) []check.Check {
	return []check.Check{
		mountRaceCheck(),
		hostMountHealthCheck(),
	}
}
