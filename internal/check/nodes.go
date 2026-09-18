package check

import (
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// Node-set shorthands for Check.Nodes: which host(s) a catalogue row runs on.
var (
	PiOnly    = []config.Node{config.NodePi}
	NASOnly   = []config.Node{config.NodeNAS}
	BothNodes = []config.Node{config.NodePi, config.NodeNAS}
)

// Cadence constants for Check.Cadence: how often the Phase 3 daemon
// schedules a check.
const (
	Every5m  = 5 * time.Minute
	Every15m = 15 * time.Minute
	Hourly   = time.Hour
	Daily    = 24 * time.Hour
)
