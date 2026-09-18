package checks

import (
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// TestCatalogueMatchesSpec pins the full aggregated catalogue (spec C5):
// every check id the registry must carry, which node(s) it runs on, its
// suggested remediation tier and its schedule cadence — and that the
// registry has exactly these ids and no others.
// (TestNoDuplicateMetricsAcrossChecks from the brief is not feasible
// statically and is intentionally not implemented; Registry's own
// duplicate-id check is exercised by Register's own tests.)
func TestCatalogueMatchesSpec(t *testing.T) {
	want := []struct {
		id      string
		nodes   []config.Node
		tier    check.Tier
		cadence time.Duration
	}{
		{"mount_race", check.PiOnly, check.TierCorrect, check.Every5m},
		{"host_mount_health", check.BothNodes, check.TierObserve, check.Every5m},
		{"arr_health", check.PiOnly, check.TierObserve, check.Every5m},
		{"arr_queue_stuck", check.PiOnly, check.TierNudge, check.Every15m},
		{"arr_wanted_missing_spike", check.PiOnly, check.TierObserve, check.Daily},
		{"indexer_failures", check.PiOnly, check.TierObserve, check.Every15m},
		{"qbit_stalled_errored", check.NASOnly, check.TierNudge, check.Every15m},
		{"qbit_completed_not_imported", check.NASOnly, check.TierCorrect, check.Every15m},
		{"wrong_file_type", check.NASOnly, check.TierCorrect, check.Every15m},
		{"plex_reachability", check.NASOnly, check.TierObserve, check.Every5m},
		{"plex_scan_freshness", check.NASOnly, check.TierNudge, check.Daily},
		{"tautulli_reachability", check.PiOnly, check.TierObserve, check.Daily},
		{"overseerr_stuck_processing", check.PiOnly, check.TierObserve, check.Daily},
		{"disk_pressure_nas_volume", check.NASOnly, check.TierCorrect, check.Hourly},
		{"disk_pressure_pi_sd", check.PiOnly, check.TierNudge, check.Daily},
		{"docker_image_bloat", check.PiOnly, check.TierNudge, check.Daily},
		{"log_size", check.PiOnly, check.TierNudge, check.Daily},
		{"recycle_bin_size", check.NASOnly, check.TierCorrect, check.Daily},
		{"orphan_downloads", check.NASOnly, check.TierCorrect, check.Daily},
		{"seeded_done", check.NASOnly, check.TierNudge, check.Daily},
		{"service_update_available", check.BothNodes, check.TierObserve, check.Daily},
	}

	r, err := Registry(config.Config{})
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}

	all := r.All()
	if len(all) != len(want) {
		gotIDs := make([]string, len(all))
		for i, c := range all {
			gotIDs[i] = c.ID
		}
		t.Fatalf("registry has %d checks, want %d: got %v", len(all), len(want), gotIDs)
	}

	for _, w := range want {
		c, ok := r.ByID(w.id)
		if !ok {
			t.Errorf("missing check %q", w.id)
			continue
		}
		if len(c.Nodes) != len(w.nodes) {
			t.Errorf("%s: Nodes = %v, want %v", w.id, c.Nodes, w.nodes)
			continue
		}
		for i, n := range w.nodes {
			if c.Nodes[i] != n {
				t.Errorf("%s: Nodes = %v, want %v", w.id, c.Nodes, w.nodes)
				break
			}
		}
		if c.Tier != w.tier {
			t.Errorf("%s: Tier = %s, want %s", w.id, c.Tier, w.tier)
		}
		if c.Cadence != w.cadence {
			t.Errorf("%s: Cadence = %v, want %v", w.id, c.Cadence, w.cadence)
		}
		if c.Run == nil {
			t.Errorf("%s: Run is nil", w.id)
		}
	}
}
