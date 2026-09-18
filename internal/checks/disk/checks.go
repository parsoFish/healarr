// Package disk implements the disk and filesystem pressure check family:
// space pressure on the Pi's SD card and the NAS's storage volume, Docker
// image bloat, log and recycle-bin directory growth, and downloads no
// longer tracked by qBittorrent.
package disk

import (
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// Checks returns this family's catalogue rows: ids, node placement, tier
// and cadence are fixed at construction, but thresholds and path lists are
// resolved from check.Deps.Cfg at Run time (see pressure.go and
// dir_size.go), so one catalogue row keeps reading whichever config the
// runner hands it rather than a snapshot taken here. cfg is accepted only
// to keep this constructor's signature consistent with every other check
// family; this family's checks do not read it.
func Checks(cfg config.Config) []check.Check {
	return []check.Check{
		pressure(diskPressurePiSDID, check.PiOnly, check.TierNudge, check.Daily, cfg),
		pressure(diskPressureNASVolumeID, check.NASOnly, check.TierCorrect, check.Hourly, cfg),
		imageBloatCheck(),
		dirSize(logSizeID, check.PiOnly, check.TierNudge, check.Daily, logDirs, logWarnGB, "log directory"),
		dirSize(recycleBinSizeID, check.NASOnly, check.TierCorrect, check.Daily, recycleDirs, recycleWarnGB, "recycle bin"),
		orphanDownloadsCheck(),
	}
}
