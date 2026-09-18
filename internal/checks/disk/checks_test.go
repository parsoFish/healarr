package disk

import (
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// TestChecksCatalogue pins the family's catalogue metadata: ids, node
// placement, tier and cadence, per the brief.
func TestChecksCatalogue(t *testing.T) {
	want := []struct {
		id      string
		nodes   []config.Node
		tier    check.Tier
		cadence time.Duration
	}{
		{diskPressurePiSDID, check.PiOnly, check.TierNudge, check.Daily},
		{diskPressureNASVolumeID, check.NASOnly, check.TierCorrect, check.Hourly},
		{imageBloatID, check.PiOnly, check.TierNudge, check.Daily},
		{logSizeID, check.PiOnly, check.TierNudge, check.Daily},
		{recycleBinSizeID, check.NASOnly, check.TierCorrect, check.Daily},
		{orphanDownloadsID, check.NASOnly, check.TierCorrect, check.Daily},
	}
	got := Checks(config.Config{})
	if len(got) != len(want) {
		t.Fatalf("Checks() returned %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		c := got[i]
		if c.ID != w.id {
			t.Errorf("row %d: ID = %q, want %q", i, c.ID, w.id)
		}
		if len(c.Nodes) != len(w.nodes) || c.Nodes[0] != w.nodes[0] {
			t.Errorf("row %d (%s): Nodes = %v, want %v", i, c.ID, c.Nodes, w.nodes)
		}
		if c.Tier != w.tier {
			t.Errorf("row %d (%s): Tier = %s, want %s", i, c.ID, c.Tier, w.tier)
		}
		if c.Cadence != w.cadence {
			t.Errorf("row %d (%s): Cadence = %v, want %v", i, c.ID, c.Cadence, w.cadence)
		}
		if c.Run == nil {
			t.Errorf("row %d (%s): Run is nil", i, c.ID)
		}
	}
}
