package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
