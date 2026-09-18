package config

import (
	"strings"
	"testing"
	"time"
)

// TestLoadAgentDefaults proves a config with no [agent] section and no
// email.digest_at still gets the daemon's baseline schedule: an operator
// who hasn't opted into custom schedules yet gets working ones.
func TestLoadAgentDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "config.toml", minimalConfig, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := Agent{
		HeartbeatInterval: 5 * time.Minute,
		CheckpointAt:      "03:00",
		PeerStaleAfter:    15 * time.Minute,
	}
	if cfg.Agent != want {
		t.Errorf("Agent = %+v, want %+v", cfg.Agent, want)
	}
	if cfg.Email.DigestAt != "07:00" {
		t.Errorf("Email.DigestAt = %q, want %q", cfg.Email.DigestAt, "07:00")
	}
}

// TestLoadAgentOverridesAndDigestAt proves an operator's [agent] values
// and email.digest_at overlay the defaults rather than being ignored.
func TestLoadAgentOverridesAndDigestAt(t *testing.T) {
	dir := t.TempDir()
	body := minimalConfig + "\n[agent]\n" +
		"timezone = \"America/New_York\"\n" +
		"heartbeat_interval = \"1m\"\n" +
		"checkpoint_at = \"04:30\"\n" +
		"peer_stale_after = \"20m\"\n" +
		"[email]\ndigest_at = \"08:15\"\n"
	cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := Agent{
		Timezone:          "America/New_York",
		HeartbeatInterval: time.Minute,
		CheckpointAt:      "04:30",
		PeerStaleAfter:    20 * time.Minute,
	}
	if cfg.Agent != want {
		t.Errorf("Agent = %+v, want %+v", cfg.Agent, want)
	}
	if cfg.Email.DigestAt != "08:15" {
		t.Errorf("Email.DigestAt = %q, want %q", cfg.Email.DigestAt, "08:15")
	}
}

// TestLoadRejectsBadTimezone proves an [agent].timezone time.LoadLocation
// cannot resolve is caught at load time, not at the first missed schedule.
func TestLoadRejectsBadTimezone(t *testing.T) {
	dir := t.TempDir()
	body := minimalConfig + "\n[agent]\ntimezone = \"Not/AZone\"\n"
	cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	_, _, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for unknown timezone")
	}
	if !strings.Contains(err.Error(), "agent.timezone") {
		t.Errorf("error %q should name agent.timezone", err)
	}
}

// TestLoadRejectsBadDigestAt walks malformed email.digest_at values and
// proves each is rejected with a message naming the key to fix.
func TestLoadRejectsBadDigestAt(t *testing.T) {
	tests := []string{"25:00", "9:00", "12:60", "12:5", "noon", ""}
	for _, bad := range tests {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			body := minimalConfig + "\n[email]\ndigest_at = \"" + bad + "\"\n"
			cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
			writeFile(t, dir, "secrets.toml", "", 0o600)

			_, _, err := Load(cfgPath)
			if err == nil {
				t.Fatalf("expected digest_at %q to be rejected", bad)
			}
			if !strings.Contains(err.Error(), "email.digest_at") {
				t.Errorf("error %q should name email.digest_at", err)
			}
		})
	}
}

// TestLoadRejectsBadCheckpointAt mirrors TestLoadRejectsBadDigestAt for
// agent.checkpoint_at, which shares the same "HH:MM" shape.
func TestLoadRejectsBadCheckpointAt(t *testing.T) {
	dir := t.TempDir()
	body := minimalConfig + "\n[agent]\ncheckpoint_at = \"24:00\"\n"
	cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	_, _, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for bad checkpoint_at")
	}
	if !strings.Contains(err.Error(), "agent.checkpoint_at") {
		t.Errorf("error %q should name agent.checkpoint_at", err)
	}
}

// TestLoadRejectsNonPositiveHeartbeatInterval proves a zero or negative
// heartbeat_interval is rejected: the daemon ticker would never fire.
func TestLoadRejectsNonPositiveHeartbeatInterval(t *testing.T) {
	tests := []string{"0s", "-1m"}
	for _, bad := range tests {
		t.Run(bad, func(t *testing.T) {
			dir := t.TempDir()
			body := minimalConfig + "\n[agent]\nheartbeat_interval = \"" + bad + "\"\n"
			cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
			writeFile(t, dir, "secrets.toml", "", 0o600)

			_, _, err := Load(cfgPath)
			if err == nil {
				t.Fatalf("expected heartbeat_interval %q to be rejected", bad)
			}
			if !strings.Contains(err.Error(), "agent.heartbeat_interval") {
				t.Errorf("error %q should name agent.heartbeat_interval", err)
			}
		})
	}
}

// TestLocationDefaultsToLocal proves Config.Location() falls back to the
// host's local zone when agent.timezone is unset, matching the doc comment
// on Agent.Timezone.
func TestLocationDefaultsToLocal(t *testing.T) {
	cfg := Defaults()
	cfg.Node = NodePi

	loc, err := cfg.Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if loc != time.Local {
		t.Errorf("Location() = %v, want time.Local", loc)
	}
}

// TestLocationUsesConfiguredTimezone proves a valid agent.timezone
// overrides the time.Local default.
func TestLocationUsesConfiguredTimezone(t *testing.T) {
	cfg := Defaults()
	cfg.Node = NodePi
	cfg.Agent.Timezone = "America/New_York"

	loc, err := cfg.Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if loc.String() != "America/New_York" {
		t.Errorf("Location() = %v, want America/New_York", loc)
	}
}

// TestLocationRejectsBadTimezone proves Location() itself fails closed
// rather than silently falling back when Agent.Timezone can't be loaded.
func TestLocationRejectsBadTimezone(t *testing.T) {
	cfg := Defaults()
	cfg.Node = NodePi
	cfg.Agent.Timezone = "Not/AZone"

	if _, err := cfg.Location(); err == nil {
		t.Fatal("expected error for unknown timezone")
	}
}
