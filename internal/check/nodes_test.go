package check

import (
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

func TestNodeSetsAndCadenceConstants(t *testing.T) {
	if len(PiOnly) != 1 || PiOnly[0] != config.NodePi {
		t.Fatalf("PiOnly = %v, want [NodePi]", PiOnly)
	}
	if len(NASOnly) != 1 || NASOnly[0] != config.NodeNAS {
		t.Fatalf("NASOnly = %v, want [NodeNAS]", NASOnly)
	}
	if len(BothNodes) != 2 || BothNodes[0] != config.NodePi || BothNodes[1] != config.NodeNAS {
		t.Fatalf("BothNodes = %v, want [NodePi NodeNAS]", BothNodes)
	}
	if Every5m != 5*time.Minute || Every15m != 15*time.Minute || Hourly != time.Hour || Daily != 24*time.Hour {
		t.Fatalf("cadence constants wrong: 5m=%v 15m=%v hourly=%v daily=%v", Every5m, Every15m, Hourly, Daily)
	}
}
