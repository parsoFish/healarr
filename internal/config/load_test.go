package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

const minimalConfig = `
node = "pi"
[services.sonarr]
url = "http://sonarr:8989"
[peer]
listen_addr = "0.0.0.0:8090"
peer_url = "http://192.0.2.10:8090"
`

func TestLoadReadsConfigAndSecrets(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "config.toml", minimalConfig, 0o644)
	writeFile(t, dir, "secrets.toml", `sonarr_api_key = "abc"`+"\n", 0o600)

	cfg, sec, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Node != NodePi || cfg.Services.Sonarr.URL != "http://sonarr:8989" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if sec.SonarrAPIKey != "abc" {
		t.Errorf("secret not loaded: %+v", sec)
	}
	if cfg.State.DBPath == "" || cfg.LLM.Model == "" {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestLoadRejectsLooseSecretsPermissions(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "config.toml", minimalConfig, 0o644)
	writeFile(t, dir, "secrets.toml", `sonarr_api_key = "abc"`+"\n", 0o644)
	if _, _, err := Load(cfgPath); err == nil {
		t.Fatal("expected permission error for 0644 secrets")
	}
}

func TestLoadRejectsUnknownNode(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "config.toml", `node = "toaster"`+"\n", 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)
	if _, _, err := Load(cfgPath); err == nil {
		t.Fatal("expected error for unknown node")
	}
}

func TestEnvOverridesWin(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "config.toml", minimalConfig, 0o644)
	writeFile(t, dir, "secrets.toml", `sonarr_api_key = "abc"`+"\n", 0o600)
	t.Setenv("HEALARR_SONARR_URL", "http://override:1")
	t.Setenv("HEALARR_SONARR_API_KEY", "fromenv")
	cfg, sec, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services.Sonarr.URL != "http://override:1" || sec.SonarrAPIKey != "fromenv" {
		t.Errorf("env override not applied: %+v %+v", cfg.Services.Sonarr, sec)
	}
}

func TestLoadResolvesArrKeyFromConfigXML(t *testing.T) {
	dir := t.TempDir()
	xml := writeFile(t, dir, "config.xml", `<Config><ApiKey>fromxml</ApiKey></Config>`, 0o644)
	cfgPath := writeFile(t, dir, "config.toml", "node = \"pi\"\n[services.sonarr]\nurl = \"http://s\"\napi_key_file = \""+xml+"\"\n", 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)
	_, sec, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if sec.SonarrAPIKey != "fromxml" {
		t.Errorf("want key from xml, got %q", sec.SonarrAPIKey)
	}
}

func TestLoadHealarrSecretsEnvOverridesSecretsPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "config.toml", minimalConfig, 0o644)
	// A secrets.toml next to config.toml with a different value — must be ignored
	// once HEALARR_SECRETS points elsewhere.
	writeFile(t, dir, "secrets.toml", `sonarr_api_key = "local"`+"\n", 0o600)

	otherDir := t.TempDir()
	overridePath := writeFile(t, otherDir, "override-secrets.toml", `sonarr_api_key = "fromoverride"`+"\n", 0o600)
	t.Setenv(secretsEnv, overridePath)

	_, sec, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sec.SonarrAPIKey != "fromoverride" {
		t.Errorf("want secret from HEALARR_SECRETS path, got %q", sec.SonarrAPIKey)
	}
}

func TestLoadErrorsWhenSecretsFileMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "config.toml", minimalConfig, 0o644)
	// No secrets.toml written next to config.toml, and no override configured.
	t.Setenv(secretsEnv, "")

	_, _, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error when secrets file is missing")
	}
	if !strings.Contains(err.Error(), "secrets file") {
		t.Errorf("expected error to mention %q, got %q", "secrets file", err.Error())
	}
}

func TestLoadChecksDefaultsAndOverride(t *testing.T) {
	dir := t.TempDir()
	body := minimalConfig + "\n[checks]\nqueue_stuck_after = \"6h\"\ndisk_paths = [\"/volume1\"]\n"
	cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Checks.QueueStuckAfter != 6*time.Hour {
		t.Errorf("QueueStuckAfter = %v, want %v", cfg.Checks.QueueStuckAfter, 6*time.Hour)
	}
	wantPaths := []string{"/volume1"}
	if len(cfg.Checks.DiskPaths) != len(wantPaths) || cfg.Checks.DiskPaths[0] != wantPaths[0] {
		t.Errorf("DiskPaths = %v, want %v", cfg.Checks.DiskPaths, wantPaths)
	}
	if cfg.Checks.DiskWarnPercent != 85 {
		t.Errorf("DiskWarnPercent = %v, want untouched default 85", cfg.Checks.DiskWarnPercent)
	}
}

// TestLoadDecodesPlexLibrariesAndRecycleDirs proves the inline-table-array
// TOML shape for [checks].plex_libraries (a list of {title, path} tables)
// round-trips through BurntSushi/toml into []config.PlexLibrary, alongside
// a plain string-array field (recycle_dirs) in the same table.
func TestLoadDecodesPlexLibrariesAndRecycleDirs(t *testing.T) {
	dir := t.TempDir()
	body := minimalConfig + "\n[checks]\n" +
		"plex_libraries = [\n" +
		"  { title = \"TV Shows\", path = \"/volume1/tv\" },\n" +
		"  { title = \"Movies\", path = \"/volume1/movies\" },\n" +
		"]\n" +
		"recycle_dirs = [\"/volume1/#recycle\"]\n"
	cfgPath := writeFile(t, dir, "config.toml", body, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantLibraries := []PlexLibrary{
		{Title: "TV Shows", Path: "/volume1/tv"},
		{Title: "Movies", Path: "/volume1/movies"},
	}
	if len(cfg.Checks.PlexLibraries) != len(wantLibraries) {
		t.Fatalf("PlexLibraries = %+v, want %+v", cfg.Checks.PlexLibraries, wantLibraries)
	}
	for i, want := range wantLibraries {
		if cfg.Checks.PlexLibraries[i] != want {
			t.Errorf("PlexLibraries[%d] = %+v, want %+v", i, cfg.Checks.PlexLibraries[i], want)
		}
	}

	wantRecycleDirs := []string{"/volume1/#recycle"}
	if len(cfg.Checks.RecycleDirs) != len(wantRecycleDirs) || cfg.Checks.RecycleDirs[0] != wantRecycleDirs[0] {
		t.Errorf("RecycleDirs = %v, want %v", cfg.Checks.RecycleDirs, wantRecycleDirs)
	}
}

func TestEnvOverrideIgnoresEmptyValue(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeFile(t, dir, "config.toml", minimalConfig, 0o644)
	writeFile(t, dir, "secrets.toml", "", 0o600)
	t.Setenv("HEALARR_SONARR_URL", "")

	cfg, _, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services.Sonarr.URL != "http://sonarr:8989" {
		t.Errorf("empty env var should not override file value, got %q", cfg.Services.Sonarr.URL)
	}
}

// TestValidateAcceptsDefaults proves the shipped defaults are themselves a
// valid config: a rejection rule that fires on Defaults() would make
// healarr unstartable out of the box.
func TestValidateAcceptsDefaults(t *testing.T) {
	cfg := Defaults()
	cfg.Node = NodePi
	if err := validate(cfg); err != nil {
		t.Fatalf("Defaults() must validate, got %v", err)
	}
}

// TestLoadRejectsBadChecksThresholds walks the [checks] thresholds the
// check engine cannot act on and proves each rejection names the TOML key
// the operator typed, so the message points at the line to fix.
func TestLoadRejectsBadChecksThresholds(t *testing.T) {
	tests := []struct {
		name    string
		checks  string
		wantKey string
	}{
		{"zero timeout", "timeout = \"0s\"\n", "checks.timeout"},
		{"negative timeout", "timeout = \"-1s\"\n", "checks.timeout"},
		{"negative duration", "orphan_after = \"-168h\"\n", "checks.orphan_after"},
		{"zero disk warn percent", "disk_warn_percent = 0\n", "checks.disk_warn_percent"},
		{"negative disk warn percent", "disk_warn_percent = -5\n", "checks.disk_warn_percent"},
		{"warn above crit", "disk_warn_percent = 95\ndisk_crit_percent = 90\n", "checks.disk_crit_percent"},
		{"crit above 100", "disk_crit_percent = 120\n", "checks.disk_crit_percent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := writeFile(t, dir, "config.toml", minimalConfig+"\n[checks]\n"+tt.checks, 0o644)
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

// TestValidateRejectsEveryNegativeChecksDuration walks every duration
// field in Checks by reflection rather than by hand, so a duration added
// later is covered — and left out of validate's own list, caught.
func TestValidateRejectsEveryNegativeChecksDuration(t *testing.T) {
	base := Defaults()
	base.Node = NodePi
	checksType := reflect.TypeOf(base.Checks)
	durationType := reflect.TypeOf(time.Duration(0))

	for i := range checksType.NumField() {
		field := checksType.Field(i)
		if field.Type != durationType {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			cfg := base
			reflect.ValueOf(&cfg.Checks).Elem().Field(i).SetInt(int64(-time.Second))

			err := validate(cfg)
			if err == nil {
				t.Fatalf("a negative %s must be rejected", field.Name)
			}
			wantKey := "checks." + field.Tag.Get("toml")
			if !strings.Contains(err.Error(), wantKey) {
				t.Errorf("error %q should name %s", err, wantKey)
			}
		})
	}
}
