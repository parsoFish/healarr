package config

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestLoadStalenessDefaultsAndCleanupActionsDefaults proves a config with
// no [staleness], [cleanup] or [actions] section still gets the C6
// baseline weights, a cautious cleanup executor (dry_run true), and the
// actions gate closed (enabled false) — an operator who hasn't opted into
// any of these yet gets safe defaults, not zero values.
func TestLoadStalenessDefaultsAndCleanupActionsDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "config.toml", minimalConfig, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantStaleness := Staleness{
		DaysMaxPoints:         40,
		DaysHorizon:           180,
		NeverWatchedAfterDays: 30,
		WatchedFullPoints:     15,
		WatchedPartialPoints:  -10,
		EndedPoints:           10,
		ContinuingPoints:      -15,
		SizePointsPerGB:       0.1,
		SizeMaxPoints:         15,
		OtherRequesterPoints:  10,
		OwnerRequesterPoints:  -5,
		AgePointsPerDay:       0.05,
		AgeMaxPoints:          10,
		CandidateThreshold:    70,
		WatchlistThreshold:    50,
		SnoozeDays:            60,
	}
	if cfg.Staleness != wantStaleness {
		t.Errorf("Staleness = %+v, want %+v", cfg.Staleness, wantStaleness)
	}

	wantCleanup := Cleanup{
		DryRun:         true,
		OrphanMinAge:   168 * time.Hour,
		RecycleMinAge:  24 * time.Hour,
		DockerDangling: true,
	}
	if cfg.Cleanup != wantCleanup {
		t.Errorf("Cleanup = %+v, want %+v", cfg.Cleanup, wantCleanup)
	}

	if cfg.Actions != (Actions{Enabled: false}) {
		t.Errorf("Actions = %+v, want disabled", cfg.Actions)
	}
}

// TestLoadStalenessOverrides proves an operator's [staleness] values
// overlay the defaults rather than being ignored, including the owner
// string (whose zero value, "", is also the default, so a real value must
// round-trip to prove it was actually read).
func TestLoadStalenessOverrides(t *testing.T) {
	dir := t.TempDir()
	body := minimalConfig + "\n[staleness]\n" +
		"days_max_points = 30\n" +
		"days_horizon = 90\n" +
		"never_watched_after_days = 14\n" +
		"watched_full_points = 20\n" +
		"watched_partial_points = -5\n" +
		"ended_points = 5\n" +
		"continuing_points = -20\n" +
		"size_points_per_gb = 0.2\n" +
		"size_max_points = 25\n" +
		"other_requester_points = 12\n" +
		"owner_requester_points = -8\n" +
		"age_points_per_day = 0.1\n" +
		"age_max_points = 20\n" +
		"candidate_threshold = 80\n" +
		"watchlist_threshold = 60\n" +
		"owner = \"jdoe@example.com\"\n" +
		"snooze_days = 30\n"
	cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := Staleness{
		DaysMaxPoints:         30,
		DaysHorizon:           90,
		NeverWatchedAfterDays: 14,
		WatchedFullPoints:     20,
		WatchedPartialPoints:  -5,
		EndedPoints:           5,
		ContinuingPoints:      -20,
		SizePointsPerGB:       0.2,
		SizeMaxPoints:         25,
		OtherRequesterPoints:  12,
		OwnerRequesterPoints:  -8,
		AgePointsPerDay:       0.1,
		AgeMaxPoints:          20,
		CandidateThreshold:    80,
		WatchlistThreshold:    60,
		Owner:                 "jdoe@example.com",
		SnoozeDays:            30,
	}
	if cfg.Staleness != want {
		t.Errorf("Staleness = %+v, want %+v", cfg.Staleness, want)
	}
}

// TestLoadCleanupOverrides proves [cleanup] values overlay the defaults.
func TestLoadCleanupOverrides(t *testing.T) {
	dir := t.TempDir()
	body := minimalConfig + "\n[cleanup]\n" +
		"dry_run = false\n" +
		"orphan_min_age = \"48h\"\n" +
		"recycle_min_age = \"6h\"\n" +
		"docker_dangling = false\n"
	cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := Cleanup{
		DryRun:         false,
		OrphanMinAge:   48 * time.Hour,
		RecycleMinAge:  6 * time.Hour,
		DockerDangling: false,
	}
	if cfg.Cleanup != want {
		t.Errorf("Cleanup = %+v, want %+v", cfg.Cleanup, want)
	}
}

// TestLoadActionsOverride proves [actions].enabled overlays the default,
// which is the whole point of the gate: an operator who has decided to
// trust healarr's decisions can flip it to true.
func TestLoadActionsOverride(t *testing.T) {
	dir := t.TempDir()
	body := minimalConfig + "\n[actions]\nenabled = true\n"
	cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Actions.Enabled {
		t.Error("Actions.Enabled = false, want true")
	}
}

// TestLoadRejectsBadStalenessThresholds walks the [staleness] threshold
// and snooze_days values the decision workflow could not act on, and
// proves each rejection names the TOML key to fix.
func TestLoadRejectsBadStalenessThresholds(t *testing.T) {
	tests := []struct {
		name      string
		staleness string
		wantKey   string
	}{
		{"zero watchlist", "watchlist_threshold = 0\n", "staleness.watchlist_threshold"},
		{"negative watchlist", "watchlist_threshold = -5\n", "staleness.watchlist_threshold"},
		{"candidate equal watchlist", "candidate_threshold = 50\nwatchlist_threshold = 50\n", "staleness.candidate_threshold"},
		{"candidate below watchlist", "candidate_threshold = 40\nwatchlist_threshold = 50\n", "staleness.candidate_threshold"},
		{"candidate above 100", "candidate_threshold = 150\n", "staleness.candidate_threshold"},
		{"zero snooze_days", "snooze_days = 0\n", "staleness.snooze_days"},
		{"negative snooze_days", "snooze_days = -1\n", "staleness.snooze_days"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := writeFile(t, dir, "config.toml", minimalConfig+"\n[staleness]\n"+tt.staleness, 0o644)
			writeFile(t, dir, "secrets.toml", "", 0o600)

			_, _, err := Load(cfgPath)
			if err == nil {
				t.Fatalf("expected %s to be rejected", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error %q should name %s", err, tt.wantKey)
			}
		})
	}
}

// TestLoadRejectsNonFiniteStalenessPoints proves a nan/inf TOML float
// literal in a [staleness] point weight is rejected rather than silently
// poisoning the C6 score.
func TestLoadRejectsNonFiniteStalenessPoints(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantKey string
	}{
		{"nan days_max_points", "days_max_points = nan\n", "staleness.days_max_points"},
		{"inf size_max_points", "size_max_points = inf\n", "staleness.size_max_points"},
		{"-inf age_max_points", "age_max_points = -inf\n", "staleness.age_max_points"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := writeFile(t, dir, "config.toml", minimalConfig+"\n[staleness]\n"+tt.value, 0o644)
			writeFile(t, dir, "secrets.toml", "", 0o600)

			_, _, err := Load(cfgPath)
			if err == nil {
				t.Fatalf("expected %s to be rejected", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error %q should name %s", err, tt.wantKey)
			}
		})
	}
}

// TestValidateRejectsEveryNonFiniteStalenessFloat walks every float64
// field in Staleness by reflection rather than by hand, so a weight or
// threshold added later that isn't checked for finiteness is caught.
func TestValidateRejectsEveryNonFiniteStalenessFloat(t *testing.T) {
	base := Defaults()
	base.Node = NodePi
	stalenessType := reflect.TypeOf(base.Staleness)
	floatType := reflect.TypeOf(float64(0))

	for i := range stalenessType.NumField() {
		field := stalenessType.Field(i)
		if field.Type != floatType {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			cfg := base
			reflect.ValueOf(&cfg.Staleness).Elem().Field(i).SetFloat(math.NaN())

			err := validate(cfg)
			if err == nil {
				t.Fatalf("a non-finite %s must be rejected", field.Name)
			}
			wantKey := "staleness." + field.Tag.Get("toml")
			if !strings.Contains(err.Error(), wantKey) {
				t.Errorf("error %q should name %s", err, wantKey)
			}
		})
	}
}

// TestLoadRejectsBadCleanupDurations proves a negative [cleanup] duration
// is rejected, naming the TOML key to fix.
func TestLoadRejectsBadCleanupDurations(t *testing.T) {
	tests := []struct {
		name    string
		cleanup string
		wantKey string
	}{
		{"negative orphan_min_age", "orphan_min_age = \"-1h\"\n", "cleanup.orphan_min_age"},
		{"negative recycle_min_age", "recycle_min_age = \"-1h\"\n", "cleanup.recycle_min_age"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := writeFile(t, dir, "config.toml", minimalConfig+"\n[cleanup]\n"+tt.cleanup, 0o644)
			writeFile(t, dir, "secrets.toml", "", 0o600)

			_, _, err := Load(cfgPath)
			if err == nil {
				t.Fatalf("expected %s to be rejected", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error %q should name %s", err, tt.wantKey)
			}
		})
	}
}

// TestLoadAcceptsZeroCleanupDurations proves a zero [cleanup] duration is
// valid (>= 0, unlike checks.timeout's > 0): a zero orphan_min_age or
// recycle_min_age just means "eligible immediately", which is a legitimate
// operator choice, not a typo.
func TestLoadAcceptsZeroCleanupDurations(t *testing.T) {
	dir := t.TempDir()
	body := minimalConfig + "\n[cleanup]\norphan_min_age = \"0s\"\nrecycle_min_age = \"0s\"\n"
	cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	if _, _, err := Load(cfgPath); err != nil {
		t.Fatalf("Load: %v", err)
	}
}
