# Healarr Phase 1 — CLI skeleton + service clients Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Python scaffold with a Go module that builds one static `healarr` binary exposing a uniform `--json` CLI over Sonarr, Radarr, Prowlarr, qBittorrent, Plex, Tautulli, Overseerr, Docker and the host filesystem.

**Architecture:** `cmd/healarr` is a thin cobra entry point. `internal/config` loads `config.toml` + `secrets.toml` (+ `HEALARR_*` env overrides) and resolves *arr API keys from their `config.xml` when asked. Each service has its own package under `internal/clients/<svc>` exposing a small Go interface with our own types, a `net/http` implementation built on `internal/clients/httpx`, and an in-memory fake used by tests and later by the check engine. `internal/cli` wires verbs to clients through a `Deps` struct so commands are testable without the network.

**Tech Stack:** Go 1.24, `github.com/spf13/cobra`, `github.com/BurntSushi/toml`, standard library `net/http` + `httptest`. No cgo. Later phases add `modernc.org/sqlite`, `robfig/cron/v3`, `anthropic-sdk-go`.

**Spec:** `docs/superpowers/specs/2026-09-12-go-rewrite-design.md`

## Global Constraints

- Module path `github.com/parsoFish/healarr`; `go 1.24` in `go.mod`.
- Every build must pass `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` and `GOARCH=amd64`.
- Files under 400 lines; one responsibility per file; packages organised by feature.
- Never mutate inputs — return new values. Errors are always returned, wrapped with `fmt.Errorf("...: %w", err)`; nothing is silently swallowed.
- No hardcoded hosts, ports, paths or credentials in Go code; defaults live in `internal/config/defaults.go` and are overridable.
- Secrets file must be mode `0600` or loading fails.
- API keys for Sonarr/Radarr/Prowlarr may be given directly in `secrets.toml` **or** resolved from `<ApiKey>` in the service's `config.xml` (`api_key_file` in config) — simplarr's rule that keys live in service config files.
- Tests: `go test ./... -race -cover`, target ≥ 80 % per non-trivial package. Every client has an `httptest` test with a golden JSON fixture in `testdata/`.
- Commits: conventional (`feat:`, `test:`, `chore:`, `docs:`), no AI attribution lines (user rule).

---

### Task 1: Go module skeleton, cobra root, CI, remove Python scaffold

**Files:**
- Delete: `healarr/` (whole Python package), `tests/`, `pyproject.toml`, `Dockerfile`, `docker-compose.yml`, `.env.example`
- Create: `go.mod`, `cmd/healarr/main.go`, `internal/cli/root.go`, `internal/cli/root_test.go`, `internal/version/version.go`, `Makefile`, `.golangci.yml`
- Modify: `.gitignore` (add `/dist/`, `*.db`, `secrets.toml`), `.github/workflows/ci.yml` (replace Python job)

**Interfaces:**
- Produces: `cli.NewRootCmd(deps *cli.Deps) *cobra.Command`; `cli.Deps` struct (fields added by later tasks); `version.Version`, `version.Commit` (ldflags-settable strings); global persistent flags `--config <path>` (default `/etc/healarr/config.toml`), `--json` (bool), `--dry-run` (bool).

- [ ] **Step 1: Remove the Python scaffold and init the module**

```bash
git rm -rq healarr tests pyproject.toml Dockerfile docker-compose.yml .env.example
go mod init github.com/parsoFish/healarr
go get github.com/spf13/cobra@v1.9.1
```

- [ ] **Step 2: Write the failing root command test**

`internal/cli/root_test.go`:
```go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootVersionPrintsVersion(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCmd(&Deps{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "healarr ") {
		t.Fatalf("expected version line, got %q", out.String())
	}
}

func TestRootHasGlobalFlags(t *testing.T) {
	cmd := NewRootCmd(&Deps{})
	for _, name := range []string{"config", "json", "dry-run"} {
		if cmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("missing persistent flag --%s", name)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestRoot -v`
Expected: FAIL — `undefined: NewRootCmd`, `undefined: Deps`

- [ ] **Step 4: Implement version, Deps, root command, main**

`internal/version/version.go`:
```go
// Package version holds build metadata injected via -ldflags.
package version

// Version and Commit are set at build time:
//   -ldflags "-X github.com/parsoFish/healarr/internal/version.Version=v0.1.0 -X ...Commit=abc123"
var (
	Version = "dev"
	Commit  = "unknown"
)
```

`internal/cli/root.go`:
```go
// Package cli wires cobra commands to service clients.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/version"
)

// Deps carries every external dependency a command may need. Tests inject
// fakes; main wires real clients lazily from config (see Task 11).
type Deps struct{}

// GlobalFlags are the persistent flags every subcommand can read.
type GlobalFlags struct {
	ConfigPath string
	JSON       bool
	DryRun     bool
}

const defaultConfigPath = "/etc/healarr/config.toml"

// NewRootCmd builds the root command tree.
func NewRootCmd(deps *Deps) *cobra.Command {
	flags := &GlobalFlags{}
	root := &cobra.Command{
		Use:           "healarr",
		Short:         "Health agent and CLI for Plex/*arr media stacks",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flags.ConfigPath, "config", defaultConfigPath, "path to config.toml")
	root.PersistentFlags().BoolVar(&flags.JSON, "json", false, "emit JSON instead of tables")
	root.PersistentFlags().BoolVar(&flags.DryRun, "dry-run", false, "never mutate anything; print what would happen")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version and commit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "healarr %s (%s)\n", version.Version, version.Commit)
			return err
		},
	})
	return root
}
```

`cmd/healarr/main.go`:
```go
// Command healarr is the single binary: CLI + agent daemon.
package main

import (
	"fmt"
	"os"

	"github.com/parsoFish/healarr/internal/cli"
)

func main() {
	if err := cli.NewRootCmd(&cli.Deps{}).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "healarr:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run tests, vet, cross-compile**

Run: `go test ./... -race -cover && go vet ./... && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/healarr && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/healarr`
Expected: PASS, both builds succeed.

- [ ] **Step 6: Makefile, golangci config, CI workflow, gitignore**

`Makefile`:
```make
VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT  ?= $(shell git rev-parse --short HEAD)
LDFLAGS := -s -w -X github.com/parsoFish/healarr/internal/version.Version=$(VERSION) -X github.com/parsoFish/healarr/internal/version.Commit=$(COMMIT)

.PHONY: build build-pi build-nas test lint

build:
	go build -ldflags '$(LDFLAGS)' -o dist/healarr ./cmd/healarr

build-pi:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o dist/healarr-linux-arm64 ./cmd/healarr

build-nas:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o dist/healarr-linux-amd64 ./cmd/healarr

test:
	go test ./... -race -cover

lint:
	go vet ./...
	golangci-lint run ./...
```

`.golangci.yml`:
```yaml
version: "2"
linters:
  default: standard
  enable: [errcheck, govet, staticcheck, unused, ineffassign, misspell, gocyclo]
  settings:
    gocyclo:
      min-complexity: 15
```

`.github/workflows/ci.yml` (replace file contents):
```yaml
name: ci
on:
  push: { branches: [main] }
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.24" }
      - run: go vet ./...
      - run: go test ./... -race -cover
      - uses: golangci/golangci-lint-action@v8
        with: { version: latest }
  build:
    runs-on: ubuntu-latest
    needs: test
    strategy:
      matrix: { arch: [arm64, amd64] }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: "1.24" }
      - run: CGO_ENABLED=0 GOOS=linux GOARCH=${{ matrix.arch }} go build -o dist/healarr-linux-${{ matrix.arch }} ./cmd/healarr
      - uses: actions/upload-artifact@v4
        with: { name: healarr-linux-${{ matrix.arch }}, path: dist/ }
```

Append to `.gitignore`:
```
/dist/
*.db
*.db-wal
*.db-shm
secrets.toml
```

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "chore: replace Python scaffold with Go module and cobra root"
```

---

### Task 2: Config loading (TOML + secrets + env overrides + arr key resolution)

**Files:**
- Create: `internal/config/config.go`, `internal/config/defaults.go`, `internal/config/load.go`, `internal/config/arrkey.go`, `internal/config/load_test.go`, `internal/config/arrkey_test.go`, `config.example.toml`, `secrets.example.toml`

**Interfaces:**
- Produces:
  - `type Node string` with consts `NodePi Node = "pi"`, `NodeNAS Node = "nas"`.
  - `type Service struct { URL string; APIKeyFile string }` (toml `url`, `api_key_file`).
  - `type Config struct { Node Node; Services Services; Peer Peer; Web Web; Email Email; LLM LLM; State State; Docker Docker; Mounts []Mount }`
  - `type Services struct { Sonarr, Radarr, Prowlarr, Overseerr, QBittorrent, Plex, Tautulli Service }`
  - `type Peer struct { ListenAddr string; PeerURL string }`, `type Web struct { ListenAddr string; BasePath string }`, `type Email struct { To string; From string; MsmtpPath string }`, `type LLM struct { Model string; DailyBudgetUSD float64; Enabled bool }`, `type State struct { DBPath string }`, `type Docker struct { Socket string; Binary string }`, `type Mount struct { Host string; Container string; ContainerPath string }`
  - `type Secrets struct { SonarrAPIKey, RadarrAPIKey, ProwlarrAPIKey, OverseerrAPIKey, QBitUser, QBitPass, PlexToken, TautulliAPIKey, PeerToken, WebToken, AnthropicAPIKey string }`
  - `func Load(configPath string) (Config, Secrets, error)` — reads config, reads `secrets.toml` next to it (path override `HEALARR_SECRETS`), checks mode 0600, applies env overrides, resolves arr keys via `api_key_file` when the secret is empty.
  - `func ReadArrAPIKey(configXMLPath string) (string, error)`.

- [ ] **Step 1: Write failing tests**

`internal/config/load_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
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
```

`internal/config/arrkey_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadArrAPIKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.xml")
	os.WriteFile(p, []byte("<Config>\n  <ApiKey>0123abcd</ApiKey>\n</Config>"), 0o644)
	got, err := ReadArrAPIKey(p)
	if err != nil || got != "0123abcd" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ReadArrAPIKey(filepath.Join(t.TempDir(), "missing.xml")); err == nil {
		t.Fatal("expected error for missing file")
	}
	empty := filepath.Join(t.TempDir(), "empty.xml")
	os.WriteFile(empty, []byte("<Config></Config>"), 0o644)
	if _, err := ReadArrAPIKey(empty); err == nil {
		t.Fatal("expected error when ApiKey element missing")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ -v`
Expected: FAIL — undefined `Load`, `ReadArrAPIKey`, `NodePi`.

- [ ] **Step 3: Implement**

`go get github.com/BurntSushi/toml@v1.5.0`

`internal/config/config.go`:
```go
// Package config loads healarr's TOML configuration and secrets.
package config

// Node identifies which host this agent runs on.
type Node string

const (
	NodePi  Node = "pi"
	NodeNAS Node = "nas"
)

// Service is one upstream HTTP service.
type Service struct {
	URL        string `toml:"url"`
	APIKeyFile string `toml:"api_key_file"` // optional: path to *arr config.xml
}

// Services groups every upstream.
type Services struct {
	Sonarr      Service `toml:"sonarr"`
	Radarr      Service `toml:"radarr"`
	Prowlarr    Service `toml:"prowlarr"`
	Overseerr   Service `toml:"overseerr"`
	QBittorrent Service `toml:"qbittorrent"`
	Plex        Service `toml:"plex"`
	Tautulli    Service `toml:"tautulli"`
}

// Peer configures the node-to-node HTTP channel.
type Peer struct {
	ListenAddr string `toml:"listen_addr"`
	PeerURL    string `toml:"peer_url"`
}

// Web configures the LAN web UI (Pi only).
type Web struct {
	ListenAddr string `toml:"listen_addr"`
	BasePath   string `toml:"base_path"`
}

// Email configures outbound mail via msmtp.
type Email struct {
	To        string `toml:"to"`
	From      string `toml:"from"`
	MsmtpPath string `toml:"msmtp_path"`
}

// LLM configures the daily digest model call.
type LLM struct {
	Enabled        bool    `toml:"enabled"`
	Model          string  `toml:"model"`
	DailyBudgetUSD float64 `toml:"daily_budget_usd"`
}

// State configures on-disk state.
type State struct {
	DBPath string `toml:"db_path"`
}

// Docker configures access to the local Docker engine.
type Docker struct {
	Socket string `toml:"socket"`
	Binary string `toml:"binary"`
}

// Mount pairs a host mount with the container path that should see it.
type Mount struct {
	Host          string `toml:"host"`
	Container     string `toml:"container"`
	ContainerPath string `toml:"container_path"`
}

// Config is the full non-secret configuration for one node.
type Config struct {
	Node     Node     `toml:"node"`
	Services Services `toml:"services"`
	Peer     Peer     `toml:"peer"`
	Web      Web      `toml:"web"`
	Email    Email    `toml:"email"`
	LLM      LLM      `toml:"llm"`
	State    State    `toml:"state"`
	Docker   Docker   `toml:"docker"`
	Mounts   []Mount  `toml:"mounts"`
}

// Secrets is everything that must never be logged or committed.
type Secrets struct {
	SonarrAPIKey    string `toml:"sonarr_api_key"`
	RadarrAPIKey    string `toml:"radarr_api_key"`
	ProwlarrAPIKey  string `toml:"prowlarr_api_key"`
	OverseerrAPIKey string `toml:"overseerr_api_key"`
	QBitUser        string `toml:"qbit_user"`
	QBitPass        string `toml:"qbit_pass"`
	PlexToken       string `toml:"plex_token"`
	TautulliAPIKey  string `toml:"tautulli_api_key"`
	PeerToken       string `toml:"peer_token"`
	WebToken        string `toml:"web_token"`
	AnthropicAPIKey string `toml:"anthropic_api_key"`
}
```

`internal/config/defaults.go`:
```go
package config

// Defaults returns the baseline configuration; Load overlays the file on it.
func Defaults() Config {
	return Config{
		Peer:   Peer{ListenAddr: "0.0.0.0:8090"},
		Web:    Web{ListenAddr: "0.0.0.0:8091", BasePath: "/healarr"},
		Email:  Email{MsmtpPath: "/usr/bin/msmtp"},
		LLM:    LLM{Enabled: true, Model: "claude-haiku-4-5-20251001", DailyBudgetUSD: 1.0},
		State:  State{DBPath: "/var/lib/healarr/state.db"},
		Docker: Docker{Socket: "/var/run/docker.sock", Binary: "docker"},
	}
}
```

`internal/config/load.go`:
```go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const secretsEnv = "HEALARR_SECRETS"

// Load reads config + secrets, applies env overrides, resolves *arr keys.
func Load(configPath string) (Config, Secrets, error) {
	cfg := Defaults()
	if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
		return Config{}, Secrets{}, fmt.Errorf("read config %s: %w", configPath, err)
	}
	secretsPath := os.Getenv(secretsEnv)
	if secretsPath == "" {
		secretsPath = filepath.Join(filepath.Dir(configPath), "secrets.toml")
	}
	sec, err := loadSecrets(secretsPath)
	if err != nil {
		return Config{}, Secrets{}, err
	}
	cfg = applyConfigEnv(cfg)
	sec = applySecretsEnv(sec)
	sec, err = resolveArrKeys(cfg, sec)
	if err != nil {
		return Config{}, Secrets{}, err
	}
	if err := validate(cfg); err != nil {
		return Config{}, Secrets{}, err
	}
	return cfg, sec, nil
}

func loadSecrets(path string) (Secrets, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Secrets{}, fmt.Errorf("secrets file: %w", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return Secrets{}, fmt.Errorf("secrets file %s has mode %o; must be 0600", path, perm)
	}
	var sec Secrets
	if _, err := toml.DecodeFile(path, &sec); err != nil {
		return Secrets{}, fmt.Errorf("read secrets %s: %w", path, err)
	}
	return sec, nil
}

func envOr(key, current string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return current
}

func applyConfigEnv(in Config) Config {
	out := in
	out.Services.Sonarr.URL = envOr("HEALARR_SONARR_URL", in.Services.Sonarr.URL)
	out.Services.Radarr.URL = envOr("HEALARR_RADARR_URL", in.Services.Radarr.URL)
	out.Services.Prowlarr.URL = envOr("HEALARR_PROWLARR_URL", in.Services.Prowlarr.URL)
	out.Services.Overseerr.URL = envOr("HEALARR_OVERSEERR_URL", in.Services.Overseerr.URL)
	out.Services.QBittorrent.URL = envOr("HEALARR_QBITTORRENT_URL", in.Services.QBittorrent.URL)
	out.Services.Plex.URL = envOr("HEALARR_PLEX_URL", in.Services.Plex.URL)
	out.Services.Tautulli.URL = envOr("HEALARR_TAUTULLI_URL", in.Services.Tautulli.URL)
	out.State.DBPath = envOr("HEALARR_STATE_DB", in.State.DBPath)
	out.Peer.PeerURL = envOr("HEALARR_PEER_URL", in.Peer.PeerURL)
	return out
}

func applySecretsEnv(in Secrets) Secrets {
	out := in
	out.SonarrAPIKey = envOr("HEALARR_SONARR_API_KEY", in.SonarrAPIKey)
	out.RadarrAPIKey = envOr("HEALARR_RADARR_API_KEY", in.RadarrAPIKey)
	out.ProwlarrAPIKey = envOr("HEALARR_PROWLARR_API_KEY", in.ProwlarrAPIKey)
	out.OverseerrAPIKey = envOr("HEALARR_OVERSEERR_API_KEY", in.OverseerrAPIKey)
	out.QBitUser = envOr("HEALARR_QBIT_USER", in.QBitUser)
	out.QBitPass = envOr("HEALARR_QBIT_PASS", in.QBitPass)
	out.PlexToken = envOr("HEALARR_PLEX_TOKEN", in.PlexToken)
	out.TautulliAPIKey = envOr("HEALARR_TAUTULLI_API_KEY", in.TautulliAPIKey)
	out.PeerToken = envOr("HEALARR_PEER_TOKEN", in.PeerToken)
	out.WebToken = envOr("HEALARR_WEB_TOKEN", in.WebToken)
	out.AnthropicAPIKey = envOr("ANTHROPIC_API_KEY", in.AnthropicAPIKey)
	return out
}

// resolveArrKeys fills empty *arr keys from their config.xml when a path is configured.
func resolveArrKeys(cfg Config, in Secrets) (Secrets, error) {
	out := in
	type slot struct {
		file string
		key  *string
		name string
	}
	slots := []slot{
		{cfg.Services.Sonarr.APIKeyFile, &out.SonarrAPIKey, "sonarr"},
		{cfg.Services.Radarr.APIKeyFile, &out.RadarrAPIKey, "radarr"},
		{cfg.Services.Prowlarr.APIKeyFile, &out.ProwlarrAPIKey, "prowlarr"},
	}
	for _, s := range slots {
		if *s.key != "" || s.file == "" {
			continue
		}
		k, err := ReadArrAPIKey(s.file)
		if err != nil {
			return Secrets{}, fmt.Errorf("%s api key: %w", s.name, err)
		}
		*s.key = k
	}
	return out, nil
}

func validate(cfg Config) error {
	switch cfg.Node {
	case NodePi, NodeNAS:
		return nil
	case "":
		return errors.New("config: node is required (\"pi\" or \"nas\")")
	default:
		return fmt.Errorf("config: unknown node %q (want \"pi\" or \"nas\")", cfg.Node)
	}
}
```

`internal/config/arrkey.go`:
```go
package config

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
)

// ReadArrAPIKey extracts <ApiKey> from a Sonarr/Radarr/Prowlarr config.xml.
func ReadArrAPIKey(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		APIKey string `xml:"ApiKey"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.APIKey == "" {
		return "", errors.New("no <ApiKey> element in " + path)
	}
	return doc.APIKey, nil
}
```

`config.example.toml`:
```toml
# healarr node configuration. Copy to /etc/healarr/config.toml (Pi) or
# /volume1/docker/healarr/config.toml (NAS). Secrets go in secrets.toml (0600).
node = "pi"                       # "pi" | "nas"

[services.sonarr]
url = "http://localhost:8989"
api_key_file = "/home/parso/.config/simplarr/sonarr/config.xml"   # or set sonarr_api_key in secrets.toml
[services.radarr]
url = "http://localhost:7878"
api_key_file = "/home/parso/.config/simplarr/radarr/config.xml"
[services.prowlarr]
url = "http://localhost:9696"
api_key_file = "/home/parso/.config/simplarr/prowlarr/config.xml"
[services.overseerr]
url = "http://localhost:5055"
[services.tautulli]
url = "http://localhost:8181"
[services.qbittorrent]
url = "http://192.0.2.10:8080"
[services.plex]
url = "https://192-0-2-10.<hash>.plex.direct:32400"   # Plex refuses plain HTTP from the LAN

[peer]
listen_addr = "0.0.0.0:8090"
peer_url = "http://192.0.2.10:8090"

[web]
listen_addr = "0.0.0.0:8091"
base_path = "/healarr"

[email]
to = "you@example.com"
from = "healarr@example.com"
msmtp_path = "/usr/bin/msmtp"

[llm]
enabled = true
model = "claude-haiku-4-5-20251001"
daily_budget_usd = 1.0

[state]
db_path = "/var/lib/healarr/state.db"

[docker]
socket = "/var/run/docker.sock"
binary = "docker"

[[mounts]]
host = "/mnt/nas/tv"
container = "sonarr"
container_path = "/tv"
[[mounts]]
host = "/mnt/nas/downloads"
container = "sonarr"
container_path = "/downloads"
[[mounts]]
host = "/mnt/nas/movies"
container = "radarr"
container_path = "/movies"
```

`secrets.example.toml`:
```toml
# chmod 600. Leave *arr keys empty to resolve from api_key_file in config.toml.
sonarr_api_key = ""
radarr_api_key = ""
prowlarr_api_key = ""
overseerr_api_key = ""
qbit_user = ""
qbit_pass = ""
plex_token = ""
tautulli_api_key = ""
peer_token = "generate-with: openssl rand -hex 32"
web_token = "generate-with: openssl rand -hex 32"
anthropic_api_key = ""
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ -race -cover -v`
Expected: PASS, coverage ≥ 85 %.

- [ ] **Step 5: Commit**

```bash
git add internal/config config.example.toml secrets.example.toml go.mod go.sum
git commit -m "feat(config): TOML config, 0600 secrets, env overrides, arr key resolution"
```

---

### Task 3: Shared JSON HTTP helper (`httpx`)

**Files:**
- Create: `internal/clients/httpx/client.go`, `internal/clients/httpx/client_test.go`

**Interfaces:**
- Produces:
  - `type Client struct` built by `New(baseURL string, opts ...Option) (*Client, error)`.
  - Options: `WithHeader(k, v string)`, `WithTimeout(d time.Duration)`, `WithHTTPClient(*http.Client)`, `WithTLSServerName(name string)`.
  - `func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, out any) error`
  - `func (c *Client) PostJSON(ctx context.Context, path string, body any, out any) error`
  - `func (c *Client) PutJSON(ctx context.Context, path string, body any, out any) error`
  - `func (c *Client) Delete(ctx context.Context, path string, query url.Values) error`
  - `func (c *Client) PostForm(ctx context.Context, path string, form url.Values, out *string) error` (qBittorrent uses form posts)
  - `type APIError struct { Status int; Method, URL, Body string }` with `Error()`; `func IsStatus(err error, status int) bool`.
  - `c.HTTP() *http.Client` (for cookie-jar sharing) and `c.BaseURL() string`.
  - `out == nil` means "discard body".

- [ ] **Step 1: Write failing tests**

`internal/clients/httpx/client_test.go`:
```go
package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestGetJSONSendsHeadersAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" || r.URL.Path != "/api/v3/health" || r.URL.Query().Get("a") != "1" {
			t.Errorf("bad request: %s %s %v", r.Method, r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"type":"warning"}]`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, WithHeader("X-Api-Key", "k"))
	if err != nil {
		t.Fatal(err)
	}
	var out []struct{ Type string }
	if err := c.GetJSON(context.Background(), "/api/v3/health", url.Values{"a": {"1"}}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Type != "warning" {
		t.Fatalf("decoded %+v", out)
	}
}

func TestNon2xxBecomesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"nope"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	err := c.GetJSON(context.Background(), "/x", nil, nil)
	if !IsStatus(err, 401) {
		t.Fatalf("want APIError 401, got %v", err)
	}
}

func TestPostFormReturnsBodyText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.FormValue("username") != "u" {
			t.Errorf("form not sent")
		}
		w.Write([]byte("Ok."))
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	var body string
	if err := c.PostForm(context.Background(), "/login", url.Values{"username": {"u"}}, &body); err != nil || body != "Ok." {
		t.Fatalf("got %q %v", body, err)
	}
}

func TestNewRejectsBadURL(t *testing.T) {
	if _, err := New("://bad"); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/clients/httpx/ -v` — Expected: FAIL, undefined `New`.

- [ ] **Step 3: Implement**

`internal/clients/httpx/client.go`:
```go
// Package httpx is a minimal JSON/form HTTP client shared by all service clients.
package httpx

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 15 * time.Second

// APIError is returned for any non-2xx response.
type APIError struct {
	Status int
	Method string
	URL    string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, truncate(e.Body, 200))
}

// IsStatus reports whether err is an APIError with the given status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

// Client wraps an *http.Client with a base URL and default headers.
type Client struct {
	base    *url.URL
	headers http.Header
	http    *http.Client
}

// Option customises a Client.
type Option func(*Client)

func WithHeader(k, v string) Option           { return func(c *Client) { c.headers.Set(k, v) } }
func WithTimeout(d time.Duration) Option      { return func(c *Client) { c.http.Timeout = d } }
func WithHTTPClient(h *http.Client) Option    { return func(c *Client) { c.http = h } }

// WithTLSServerName sets SNI/verification name (Plex's plex.direct certificates).
func WithTLSServerName(name string) Option {
	return func(c *Client) {
		t, ok := c.http.Transport.(*http.Transport)
		if !ok || t == nil {
			t = http.DefaultTransport.(*http.Transport).Clone()
		}
		t.TLSClientConfig = &tls.Config{ServerName: name, MinVersion: tls.VersionTLS12}
		c.http.Transport = t
	}
}

// New builds a client for baseURL.
func New(baseURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("httpx: invalid base URL %q", baseURL)
	}
	c := &Client{base: u, headers: http.Header{}, http: &http.Client{Timeout: defaultTimeout}}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

func (c *Client) HTTP() *http.Client { return c.http }
func (c *Client) BaseURL() string    { return c.base.String() }

func (c *Client) resolve(path string, query url.Values) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func (c *Client) do(ctx context.Context, method, rawURL string, body io.Reader, contentType string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, fmt.Errorf("httpx: build %s %s: %w", method, rawURL, err)
	}
	for k, vs := range c.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpx: %s %s: %w", method, rawURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("httpx: read %s %s: %w", method, rawURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &APIError{Status: resp.StatusCode, Method: method, URL: rawURL, Body: string(raw)}
	}
	return raw, nil
}

func decode(raw []byte, out any) error {
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("httpx: decode response: %w (body: %s)", err, truncate(string(raw), 200))
	}
	return nil
}

func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, out any) error {
	raw, err := c.do(ctx, http.MethodGet, c.resolve(path, query), nil, "")
	if err != nil {
		return err
	}
	return decode(raw, out)
}

func (c *Client) sendJSON(ctx context.Context, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return fmt.Errorf("httpx: encode body: %w", err)
		}
	}
	raw, err := c.do(ctx, method, c.resolve(path, nil), &buf, "application/json")
	if err != nil {
		return err
	}
	return decode(raw, out)
}

func (c *Client) PostJSON(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPost, path, body, out)
}

func (c *Client) PutJSON(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPut, path, body, out)
}

func (c *Client) Delete(ctx context.Context, path string, query url.Values) error {
	_, err := c.do(ctx, http.MethodDelete, c.resolve(path, query), nil, "")
	return err
}

// PostForm sends application/x-www-form-urlencoded and returns the raw text body.
func (c *Client) PostForm(ctx context.Context, path string, form url.Values, out *string) error {
	raw, err := c.do(ctx, http.MethodPost, c.resolve(path, nil), strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	if out != nil {
		*out = string(raw)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
```

- [ ] **Step 4: Run tests** — `go test ./internal/clients/httpx/ -race -cover` — Expected: PASS.

- [ ] **Step 5: Commit** — `git add internal/clients/httpx && git commit -m "feat(httpx): shared JSON/form HTTP client with typed API errors"`

---

### Task 4: Sonarr client

**Files:**
- Create: `internal/clients/sonarr/types.go`, `internal/clients/sonarr/client.go`, `internal/clients/sonarr/fake.go`, `internal/clients/sonarr/client_test.go`, `internal/clients/sonarr/testdata/{health.json,queue.json,series.json,rootfolder.json,wanted_missing.json,history.json}`

**Interfaces:**
- Produces (package `sonarr`):
```go
type HealthItem struct { Type, Source, Message string }
type QueueItem struct {
	ID int64; SeriesID int64; EpisodeID int64; Title string; Status string
	TrackedDownloadStatus, TrackedDownloadState string; DownloadID string
	OutputPath string; Size, SizeLeft float64; Messages []string; Added time.Time
}
type Series struct {
	ID int64; Title string; Monitored bool; Status string; Path string; Added time.Time
	EpisodeFileCount, EpisodeCount int; SizeOnDisk int64; TVDBID int64
}
type RootFolder struct { Path string; Accessible bool; FreeSpace int64 }
type HistoryRecord struct { ID int64; Date time.Time; EventType string; SourceTitle string; SeriesID, EpisodeID int64; DownloadID string }
type Client interface {
	Health(ctx) ([]HealthItem, error)
	Queue(ctx) ([]QueueItem, error)                       // all pages, includeUnknownSeriesItems=true
	Series(ctx) ([]Series, error)
	RootFolders(ctx) ([]RootFolder, error)
	WantedMissingCount(ctx) (int, error)                   // monitored only
	History(ctx, since time.Time, eventType string) ([]HistoryRecord, error)
	DeleteQueueItem(ctx, id int64, removeFromClient, blocklist bool) error
	UpdateSeriesMonitored(ctx, id int64, monitored bool) error
	DeleteSeries(ctx, id int64, deleteFiles, addExclusion bool) error
	RunCommand(ctx, name string, params map[string]any) (int64, error)
}
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error)
type Fake struct { HealthItems []HealthItem; QueueItems []QueueItem; SeriesList []Series; Roots []RootFolder; Missing int; HistoryRecords []HistoryRecord; Calls []string; Err error }
```
- `Fake` records every call as `"Method(args)"` in `Calls` and returns `Err` if set. Later packages depend on `Fake` heavily.
- Queue `Messages` flattens `statusMessages[].messages[]`.

- [ ] **Step 1: Capture golden fixtures** — on the Pi (keys never leave it):

```bash
ssh pi 'set -a; . ~/simplarr/.env; set +a; K=$(grep -oP "(?<=<ApiKey>)[^<]+" $DOCKER_CONFIG/sonarr/config.xml); H="X-Api-Key: $K"; B=http://localhost:8989/api/v3
curl -s -H "$H" $B/health > /tmp/health.json
curl -s -H "$H" "$B/queue?pageSize=3&includeUnknownSeriesItems=true" > /tmp/queue.json
curl -s -H "$H" $B/series | jq ".[0:3]" > /tmp/series.json
curl -s -H "$H" $B/rootfolder > /tmp/rootfolder.json
curl -s -H "$H" "$B/wanted/missing?pageSize=1&monitored=true" > /tmp/wanted_missing.json
curl -s -H "$H" "$B/history?pageSize=3&sortKey=date&sortDirection=descending" > /tmp/history.json'
mkdir -p internal/clients/sonarr/testdata
for f in health queue series rootfolder wanted_missing history; do scp -q pi:/tmp/$f.json internal/clients/sonarr/testdata/$f.json; done
grep -il "apikey" internal/clients/sonarr/testdata/* && echo "REDACT NEEDED" || echo "clean"
```
If any fixture contains a key or password, replace the value with `REDACTED` before committing.

- [ ] **Step 2: Write failing tests**

`internal/clients/sonarr/client_test.go`:
```go
package sonarr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newServer serves fixtures by path and records requests.
func newServer(t *testing.T, routes map[string]string, seen *[]*http.Request) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r)
		}
		if r.Header.Get("X-Api-Key") != "key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Method == http.MethodDelete || r.Method == http.MethodPut {
			w.WriteHeader(200)
			return
		}
		f, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, f))
	}))
}

func TestHealthDecodes(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/health": "health.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	items, err := c.Health(context.Background())
	if err != nil || len(items) == 0 || items[0].Source == "" {
		t.Fatalf("health: %+v %v", items, err)
	}
}

func TestQueueFlattensMessages(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, map[string]string{"/api/v3/queue": "queue.json"}, &seen)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	items, err := c.Queue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || items[0].DownloadID == "" {
		t.Fatalf("queue items: %+v", items)
	}
	if q := seen[0].URL.Query(); q.Get("includeUnknownSeriesItems") != "true" || q.Get("pageSize") == "" {
		t.Errorf("query params missing: %v", q)
	}
}

func TestSeriesStatistics(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/series": "series.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	s, err := c.Series(context.Background())
	if err != nil || len(s) == 0 || s[0].EpisodeCount == 0 {
		t.Fatalf("series: %+v %v", s, err)
	}
}

func TestWantedMissingCount(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/wanted/missing": "wanted_missing.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	n, err := c.WantedMissingCount(context.Background())
	if err != nil || n <= 0 {
		t.Fatalf("missing: %d %v", n, err)
	}
}

func TestHistoryFiltersSince(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/history": "history.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	all, err := c.History(context.Background(), time.Time{}, "")
	if err != nil || len(all) == 0 {
		t.Fatalf("history: %v %v", all, err)
	}
	none, _ := c.History(context.Background(), time.Now().Add(24*time.Hour), "")
	if len(none) != 0 {
		t.Errorf("since filter not applied: %d", len(none))
	}
}

func TestDeleteQueueItemQuery(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, nil, &seen)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.DeleteQueueItem(context.Background(), 42, true, true); err != nil {
		t.Fatal(err)
	}
	r := seen[0]
	if r.Method != "DELETE" || r.URL.Path != "/api/v3/queue/42" || r.URL.Query().Get("removeFromClient") != "true" || r.URL.Query().Get("blocklist") != "true" {
		t.Errorf("bad delete request: %s %s", r.Method, r.URL)
	}
}

func TestUnauthorizedSurfacesStatus(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/health": "health.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "wrong")
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("expected 401 error")
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{Missing: 3}
	n, _ := f.WantedMissingCount(context.Background())
	if n != 3 || len(f.Calls) != 1 || f.Calls[0] != "WantedMissingCount()" {
		t.Fatalf("fake: %d %v", n, f.Calls)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail** — `go test ./internal/clients/sonarr/ -v` — Expected: FAIL (undefined New/Fake).

- [ ] **Step 4: Implement types, client, fake**

`internal/clients/sonarr/types.go`:
```go
// Package sonarr is a typed client for the Sonarr v3 API.
package sonarr

import (
	"context"
	"time"
)

type HealthItem struct {
	Type    string `json:"type"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

type QueueItem struct {
	ID                    int64
	SeriesID              int64
	EpisodeID             int64
	Title                 string
	Status                string
	TrackedDownloadStatus string
	TrackedDownloadState  string
	DownloadID            string
	OutputPath            string
	Size                  float64
	SizeLeft              float64
	Messages              []string
	Added                 time.Time
}

type Series struct {
	ID               int64
	TVDBID           int64
	Title            string
	Monitored        bool
	Status           string
	Path             string
	Added            time.Time
	EpisodeFileCount int
	EpisodeCount     int
	SizeOnDisk       int64
}

type RootFolder struct {
	Path       string `json:"path"`
	Accessible bool   `json:"accessible"`
	FreeSpace  int64  `json:"freeSpace"`
}

type HistoryRecord struct {
	ID          int64     `json:"id"`
	Date        time.Time `json:"date"`
	EventType   string    `json:"eventType"`
	SourceTitle string    `json:"sourceTitle"`
	SeriesID    int64     `json:"seriesId"`
	EpisodeID   int64     `json:"episodeId"`
	DownloadID  string    `json:"downloadId"`
}

// Client is the behaviour the rest of healarr depends on.
type Client interface {
	Health(ctx context.Context) ([]HealthItem, error)
	Queue(ctx context.Context) ([]QueueItem, error)
	Series(ctx context.Context) ([]Series, error)
	RootFolders(ctx context.Context) ([]RootFolder, error)
	WantedMissingCount(ctx context.Context) (int, error)
	History(ctx context.Context, since time.Time, eventType string) ([]HistoryRecord, error)
	DeleteQueueItem(ctx context.Context, id int64, removeFromClient, blocklist bool) error
	UpdateSeriesMonitored(ctx context.Context, id int64, monitored bool) error
	DeleteSeries(ctx context.Context, id int64, deleteFiles, addExclusion bool) error
	RunCommand(ctx context.Context, name string, params map[string]any) (int64, error)
}
```

`internal/clients/sonarr/client.go`:
```go
package sonarr

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

const (
	apiBase       = "/api/v3"
	queuePageSize = 250
	historyPage   = 500
)

// HTTPClient talks to a real Sonarr.
type HTTPClient struct{ h *httpx.Client }

var _ Client = (*HTTPClient)(nil)

// New builds an HTTPClient; apiKey is sent as X-Api-Key.
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error) {
	all := append([]httpx.Option{httpx.WithHeader("X-Api-Key", apiKey)}, opts...)
	h, err := httpx.New(baseURL, all...)
	if err != nil {
		return nil, fmt.Errorf("sonarr: %w", err)
	}
	return &HTTPClient{h: h}, nil
}

func (c *HTTPClient) Health(ctx context.Context) ([]HealthItem, error) {
	var out []HealthItem
	if err := c.h.GetJSON(ctx, apiBase+"/health", nil, &out); err != nil {
		return nil, fmt.Errorf("sonarr health: %w", err)
	}
	return out, nil
}

// raw wire shapes kept private; public types stay stable.
type rawQueuePage struct {
	TotalRecords int `json:"totalRecords"`
	Records      []struct {
		ID                    int64     `json:"id"`
		SeriesID              int64     `json:"seriesId"`
		EpisodeID             int64     `json:"episodeId"`
		Title                 string    `json:"title"`
		Status                string    `json:"status"`
		TrackedDownloadStatus string    `json:"trackedDownloadStatus"`
		TrackedDownloadState  string    `json:"trackedDownloadState"`
		DownloadID            string    `json:"downloadId"`
		OutputPath            string    `json:"outputPath"`
		Size                  float64   `json:"size"`
		SizeLeft              float64   `json:"sizeleft"`
		Added                 time.Time `json:"added"`
		StatusMessages        []struct {
			Messages []string `json:"messages"`
		} `json:"statusMessages"`
	} `json:"records"`
}

func (c *HTTPClient) Queue(ctx context.Context) ([]QueueItem, error) {
	var items []QueueItem
	for page := 1; ; page++ {
		q := url.Values{
			"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(queuePageSize)},
			"includeUnknownSeriesItems": {"true"},
		}
		var p rawQueuePage
		if err := c.h.GetJSON(ctx, apiBase+"/queue", q, &p); err != nil {
			return nil, fmt.Errorf("sonarr queue page %d: %w", page, err)
		}
		for _, r := range p.Records {
			var msgs []string
			for _, sm := range r.StatusMessages {
				msgs = append(msgs, sm.Messages...)
			}
			items = append(items, QueueItem{
				ID: r.ID, SeriesID: r.SeriesID, EpisodeID: r.EpisodeID, Title: r.Title, Status: r.Status,
				TrackedDownloadStatus: r.TrackedDownloadStatus, TrackedDownloadState: r.TrackedDownloadState,
				DownloadID: r.DownloadID, OutputPath: r.OutputPath, Size: r.Size, SizeLeft: r.SizeLeft,
				Messages: msgs, Added: r.Added,
			})
		}
		if len(items) >= p.TotalRecords || len(p.Records) == 0 {
			return items, nil
		}
	}
}

type rawSeries struct {
	ID         int64     `json:"id"`
	TVDBID     int64     `json:"tvdbId"`
	Title      string    `json:"title"`
	Monitored  bool      `json:"monitored"`
	Status     string    `json:"status"`
	Path       string    `json:"path"`
	Added      time.Time `json:"added"`
	Statistics struct {
		EpisodeFileCount int   `json:"episodeFileCount"`
		EpisodeCount     int   `json:"episodeCount"`
		SizeOnDisk       int64 `json:"sizeOnDisk"`
	} `json:"statistics"`
}

func (c *HTTPClient) Series(ctx context.Context) ([]Series, error) {
	var raw []rawSeries
	if err := c.h.GetJSON(ctx, apiBase+"/series", nil, &raw); err != nil {
		return nil, fmt.Errorf("sonarr series: %w", err)
	}
	out := make([]Series, 0, len(raw))
	for _, r := range raw {
		out = append(out, Series{
			ID: r.ID, TVDBID: r.TVDBID, Title: r.Title, Monitored: r.Monitored, Status: r.Status, Path: r.Path, Added: r.Added,
			EpisodeFileCount: r.Statistics.EpisodeFileCount, EpisodeCount: r.Statistics.EpisodeCount, SizeOnDisk: r.Statistics.SizeOnDisk,
		})
	}
	return out, nil
}

func (c *HTTPClient) RootFolders(ctx context.Context) ([]RootFolder, error) {
	var out []RootFolder
	if err := c.h.GetJSON(ctx, apiBase+"/rootfolder", nil, &out); err != nil {
		return nil, fmt.Errorf("sonarr rootfolders: %w", err)
	}
	return out, nil
}

func (c *HTTPClient) WantedMissingCount(ctx context.Context) (int, error) {
	var p struct {
		TotalRecords int `json:"totalRecords"`
	}
	q := url.Values{"pageSize": {"1"}, "monitored": {"true"}}
	if err := c.h.GetJSON(ctx, apiBase+"/wanted/missing", q, &p); err != nil {
		return 0, fmt.Errorf("sonarr wanted/missing: %w", err)
	}
	return p.TotalRecords, nil
}

func (c *HTTPClient) History(ctx context.Context, since time.Time, eventType string) ([]HistoryRecord, error) {
	var out []HistoryRecord
	for page := 1; ; page++ {
		q := url.Values{
			"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(historyPage)},
			"sortKey": {"date"}, "sortDirection": {"descending"},
		}
		if eventType != "" {
			q.Set("eventType", eventType)
		}
		var p struct {
			TotalRecords int             `json:"totalRecords"`
			Records      []HistoryRecord `json:"records"`
		}
		if err := c.h.GetJSON(ctx, apiBase+"/history", q, &p); err != nil {
			return nil, fmt.Errorf("sonarr history page %d: %w", page, err)
		}
		for _, r := range p.Records {
			if !since.IsZero() && r.Date.Before(since) {
				return out, nil // sorted descending: everything after is older
			}
			out = append(out, r)
		}
		if len(p.Records) == 0 || page*historyPage >= p.TotalRecords {
			return out, nil
		}
	}
}

func (c *HTTPClient) DeleteQueueItem(ctx context.Context, id int64, removeFromClient, blocklist bool) error {
	q := url.Values{
		"removeFromClient": {strconv.FormatBool(removeFromClient)},
		"blocklist":        {strconv.FormatBool(blocklist)},
		"skipRedownload":   {"false"},
	}
	if err := c.h.Delete(ctx, fmt.Sprintf("%s/queue/%d", apiBase, id), q); err != nil {
		return fmt.Errorf("sonarr delete queue %d: %w", id, err)
	}
	return nil
}

func (c *HTTPClient) UpdateSeriesMonitored(ctx context.Context, id int64, monitored bool) error {
	// Sonarr requires the full object on PUT; fetch, patch, send.
	var full map[string]any
	if err := c.h.GetJSON(ctx, fmt.Sprintf("%s/series/%d", apiBase, id), nil, &full); err != nil {
		return fmt.Errorf("sonarr get series %d: %w", id, err)
	}
	patched := make(map[string]any, len(full))
	for k, v := range full {
		patched[k] = v
	}
	patched["monitored"] = monitored
	if err := c.h.PutJSON(ctx, fmt.Sprintf("%s/series/%d?moveFiles=false", apiBase, id), patched, nil); err != nil {
		return fmt.Errorf("sonarr update series %d: %w", id, err)
	}
	return nil
}

func (c *HTTPClient) DeleteSeries(ctx context.Context, id int64, deleteFiles, addExclusion bool) error {
	q := url.Values{
		"deleteFiles":            {strconv.FormatBool(deleteFiles)},
		"addImportListExclusion": {strconv.FormatBool(addExclusion)},
	}
	if err := c.h.Delete(ctx, fmt.Sprintf("%s/series/%d", apiBase, id), q); err != nil {
		return fmt.Errorf("sonarr delete series %d: %w", id, err)
	}
	return nil
}

func (c *HTTPClient) RunCommand(ctx context.Context, name string, params map[string]any) (int64, error) {
	body := map[string]any{"name": name}
	for k, v := range params {
		body[k] = v
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.h.PostJSON(ctx, apiBase+"/command", body, &out); err != nil {
		return 0, fmt.Errorf("sonarr command %s: %w", name, err)
	}
	return out.ID, nil
}
```

`internal/clients/sonarr/fake.go`:
```go
package sonarr

import (
	"context"
	"fmt"
	"time"
)

// Fake is an in-memory Client for tests and the check engine's table tests.
type Fake struct {
	HealthItems    []HealthItem
	QueueItems     []QueueItem
	SeriesList     []Series
	Roots          []RootFolder
	Missing        int
	HistoryRecords []HistoryRecord
	Calls          []string
	Err            error
}

var _ Client = (*Fake)(nil)

func (f *Fake) record(format string, args ...any) error {
	f.Calls = append(f.Calls, fmt.Sprintf(format, args...))
	return f.Err
}

func (f *Fake) Health(context.Context) ([]HealthItem, error) {
	return f.HealthItems, f.record("Health()")
}
func (f *Fake) Queue(context.Context) ([]QueueItem, error) { return f.QueueItems, f.record("Queue()") }
func (f *Fake) Series(context.Context) ([]Series, error)   { return f.SeriesList, f.record("Series()") }
func (f *Fake) RootFolders(context.Context) ([]RootFolder, error) {
	return f.Roots, f.record("RootFolders()")
}
func (f *Fake) WantedMissingCount(context.Context) (int, error) {
	return f.Missing, f.record("WantedMissingCount()")
}
func (f *Fake) History(_ context.Context, since time.Time, eventType string) ([]HistoryRecord, error) {
	var out []HistoryRecord
	for _, r := range f.HistoryRecords {
		if (since.IsZero() || !r.Date.Before(since)) && (eventType == "" || r.EventType == eventType) {
			out = append(out, r)
		}
	}
	return out, f.record("History(%s,%s)", since.Format(time.RFC3339), eventType)
}
func (f *Fake) DeleteQueueItem(_ context.Context, id int64, rm, bl bool) error {
	return f.record("DeleteQueueItem(%d,%t,%t)", id, rm, bl)
}
func (f *Fake) UpdateSeriesMonitored(_ context.Context, id int64, m bool) error {
	return f.record("UpdateSeriesMonitored(%d,%t)", id, m)
}
func (f *Fake) DeleteSeries(_ context.Context, id int64, df, ex bool) error {
	return f.record("DeleteSeries(%d,%t,%t)", id, df, ex)
}
func (f *Fake) RunCommand(_ context.Context, name string, _ map[string]any) (int64, error) {
	return 1, f.record("RunCommand(%s)", name)
}
```

- [ ] **Step 5: Run tests** — `go test ./internal/clients/sonarr/ -race -cover` — Expected: PASS, ≥ 80 %.

- [ ] **Step 6: Commit** — `git add internal/clients/sonarr && git commit -m "feat(sonarr): typed v3 client, fake, golden fixtures"`

---

### Task 5: Radarr client

**Files:**
- Create: `internal/clients/radarr/types.go`, `internal/clients/radarr/client.go`, `internal/clients/radarr/fake.go`, `internal/clients/radarr/client_test.go`, `internal/clients/radarr/testdata/{health.json,queue.json,movie.json,rootfolder.json,wanted_missing.json,history.json}`

**Interfaces:**
- Produces (package `radarr`):
```go
type HealthItem struct { Type, Source, Message string }          // json tags as in sonarr
type QueueItem struct { ID, MovieID int64; Title, Status, TrackedDownloadStatus, TrackedDownloadState, DownloadID, OutputPath string; Size, SizeLeft float64; Messages []string; Added time.Time }
type Movie struct { ID, TMDBID int64; Title string; Year int; Monitored, HasFile bool; Status, Path string; Added time.Time; SizeOnDisk int64 }
type RootFolder struct { Path string; Accessible bool; FreeSpace int64 }
type HistoryRecord struct { ID int64; Date time.Time; EventType, SourceTitle string; MovieID int64; DownloadID string }
type Client interface {
	Health(ctx) ([]HealthItem, error)
	Queue(ctx) ([]QueueItem, error)                  // includeUnknownMovieItems=true, all pages
	Movies(ctx) ([]Movie, error)
	RootFolders(ctx) ([]RootFolder, error)
	WantedMissingCount(ctx) (int, error)
	History(ctx, since time.Time, eventType string) ([]HistoryRecord, error)
	DeleteQueueItem(ctx, id int64, removeFromClient, blocklist bool) error
	UpdateMovieMonitored(ctx, id int64, monitored bool) error
	DeleteMovie(ctx, id int64, deleteFiles, addExclusion bool) error
	RunCommand(ctx, name string, params map[string]any) (int64, error)
}
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error)
type Fake struct { HealthItems []HealthItem; QueueItems []QueueItem; MovieList []Movie; Roots []RootFolder; Missing int; HistoryRecords []HistoryRecord; Calls []string; Err error }
```

- [ ] **Step 1: Capture fixtures** (same pattern as Task 4, port 7878, `DOCKER_CONFIG/radarr/config.xml`; `movie.json` = `curl $B/movie | jq ".[0:3]"`; queue uses `includeUnknownMovieItems=true`). Redact if needed.

- [ ] **Step 2: Write failing tests** — `internal/clients/radarr/client_test.go`: copy the Sonarr test file, renaming `Series`→`Movies`, fixture `series.json`→`movie.json`, assert `Movies()[0].Title != ""` and `HasFile` is decoded, `Queue` asserts `includeUnknownMovieItems=true`, `DeleteQueueItem` asserts path `/api/v3/queue/42`, plus `TestFakeRecordsCalls` using `Fake{Missing: 3}`.

- [ ] **Step 3: Run tests to verify they fail** — `go test ./internal/clients/radarr/ -v`.

- [ ] **Step 4: Implement** — `types.go` with the structs above (json tags: `id, tmdbId, title, year, monitored, hasFile, status, path, added, sizeOnDisk`; history `id, date, eventType, sourceTitle, movieId, downloadId`). `client.go` is the Sonarr client with these substitutions: package `radarr`; error prefixes `radarr`; queue query `includeUnknownMovieItems`; `rawQueuePage.Records[i].MovieID` from `movieId`; `Movies()` decodes `[]Movie` directly from `/api/v3/movie` (no statistics nesting); `UpdateMovieMonitored` = GET `/api/v3/movie/{id}`, copy map, set `monitored`, PUT `/api/v3/movie/{id}?moveFiles=false`; `DeleteMovie` = DELETE `/api/v3/movie/{id}?deleteFiles=&addImportExclusion=` (note Radarr's parameter is `addImportExclusion`, Sonarr's is `addImportListExclusion`). `fake.go` mirrors Sonarr's with `MovieList`/`Movies()`/`UpdateMovieMonitored`/`DeleteMovie`.

- [ ] **Step 5: Run tests** — `go test ./internal/clients/radarr/ -race -cover` — PASS ≥ 80 %.

- [ ] **Step 6: Commit** — `git add internal/clients/radarr && git commit -m "feat(radarr): typed v3 client, fake, golden fixtures"`

---

### Task 6: Prowlarr client

**Files:**
- Create: `internal/clients/prowlarr/types.go`, `internal/clients/prowlarr/client.go`, `internal/clients/prowlarr/fake.go`, `internal/clients/prowlarr/client_test.go`, `internal/clients/prowlarr/testdata/{health.json,indexer.json,indexerstatus.json}`

**Interfaces:**
- Produces:
```go
type HealthItem struct { Type, Source, Message string }
type Indexer struct { ID int64; Name string; Enable bool; Protocol string; Priority int }
type IndexerStatus struct { IndexerID int64; MostRecentFailure, DisabledTill time.Time }
type Client interface {
	Health(ctx) ([]HealthItem, error)
	Indexers(ctx) ([]Indexer, error)
	IndexerStatus(ctx) ([]IndexerStatus, error)
	DeleteIndexer(ctx, id int64) error
	TestIndexer(ctx, id int64) error            // POST /api/v1/indexer/test with the indexer object
}
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error)
type Fake struct { HealthItems []HealthItem; IndexerList []Indexer; Statuses []IndexerStatus; Calls []string; Err error }
```
- API base is `/api/v1` (Prowlarr), not v3.

- [ ] **Step 1: Capture fixtures** — port 9696, `DOCKER_CONFIG/prowlarr/config.xml`: `/api/v1/health`, `/api/v1/indexer` (strip `fields` arrays with `jq 'map(del(.fields))'` — they may contain credentials), `/api/v1/indexerstatus`.

- [ ] **Step 2: Write failing tests** — same `newServer` helper; `TestIndexersDecode` asserts `Name`+`Enable`; `TestIndexerStatusParsesTimes` asserts `DisabledTill` non-zero when fixture has one, and zero when the JSON field is null (add a hand-written second entry `{"indexerId": 9, "mostRecentFailure": null, "disabledTill": null}` to the fixture); `TestDeleteIndexer` asserts `DELETE /api/v1/indexer/7`; `TestTestIndexer` asserts `POST /api/v1/indexer/test` body contains `"id":7`; `TestFakeRecordsCalls`.

- [ ] **Step 3: Run to fail.**

- [ ] **Step 4: Implement** — nullable times decode via a private `type nullTime struct{ time.Time }` with `UnmarshalJSON` treating `null`/`""` as zero. `TestIndexer` GETs `/api/v1/indexer/{id}` into `map[string]any` then POSTs it to `/api/v1/indexer/test` (Prowlarr returns 200 on success, 400 with a JSON list of validation failures otherwise — surface as `APIError`).

- [ ] **Step 5: Run tests** — PASS ≥ 80 %.

- [ ] **Step 6: Commit** — `git commit -m "feat(prowlarr): typed v1 client, fake, fixtures"`

---

### Task 7: qBittorrent client

**Files:**
- Create: `internal/clients/qbittorrent/types.go`, `internal/clients/qbittorrent/client.go`, `internal/clients/qbittorrent/fake.go`, `internal/clients/qbittorrent/client_test.go`, `internal/clients/qbittorrent/testdata/{torrents_info.json,torrent_files.json,preferences.json}`

**Interfaces:**
- Produces:
```go
type Torrent struct { Hash, Name, State, Category, SavePath, ContentPath string; Progress, Ratio float64; Size, Completed, Downloaded, Uploaded int64; AddedOn, CompletionOn time.Time; SeedingTime time.Duration; DlSpeed, UpSpeed int64; NumSeeds, NumLeechs int }
type File struct { Index int; Name string; Size int64; Progress float64; Priority int }
type Preferences struct { SavePath, TempPath string; TempPathEnabled bool; MaxRatio float64; MaxSeedingTime int; ExcludedFileNames string }
type Client interface {
	Version(ctx) (string, error)                            // /api/v2/app/version, doubles as liveness+auth check
	Torrents(ctx, filter, category string) ([]Torrent, error) // filter "" = all; e.g. "stalled", "completed", "errored"
	Files(ctx, hash string) ([]File, error)
	Delete(ctx, hashes []string, deleteFiles bool) error
	Reannounce(ctx, hashes []string) error
	Resume(ctx, hashes []string) error
	Preferences(ctx) (Preferences, error)
	SetPreferences(ctx, patch map[string]any) error
}
func New(baseURL, user, pass string, opts ...httpx.Option) (*HTTPClient, error)
type Fake struct { TorrentList []Torrent; FilesByHash map[string][]File; Prefs Preferences; Calls []string; Err error }
```
- Auth: `POST /api/v2/auth/login` form `username`,`password`; success body `Ok.`; cookie `SID` stored in a `cookiejar` on the shared `*http.Client`. Every method calls `ensureLogin` on first use and retries **once** after a 403 (session expiry). Empty user+pass = assume subnet whitelist, skip login.
- qBittorrent returns epoch seconds; decode via private `epochTime`.
- Hash lists are joined with `|`.

- [ ] **Step 1: Capture fixtures** — requires qBit creds (`secrets.toml` on the Pi later) or the subnet whitelist; until then write fixtures by hand from the API docs shape:
`torrents_info.json`:
```json
[{"hash":"853ed40dabcdef","name":"A.Knight.S01E06.720p","state":"pausedUP","category":"tv-sonarr","save_path":"/downloads/tv","content_path":"/downloads/tv/A.Knight.S01E06.720p.exe","progress":1,"ratio":1.02,"size":2312863971,"completed":2312863971,"downloaded":2312863971,"uploaded":2359000000,"added_on":1771545600,"completion_on":1771549200,"seeding_time":172800,"dlspeed":0,"upspeed":0,"num_seeds":0,"num_leechs":0},
 {"hash":"df443a41000000","name":"Avatar S02","state":"stalledDL","category":"tv-sonarr","save_path":"/downloads/tv","content_path":"/downloads/incomplete/TV/Avatar S02","progress":0,"ratio":0,"size":17500000000,"completed":0,"downloaded":0,"uploaded":0,"added_on":1757600000,"completion_on":-1,"seeding_time":0,"dlspeed":0,"upspeed":0,"num_seeds":0,"num_leechs":3}]
```
`torrent_files.json`: `[{"index":0,"name":"A.Knight.S01E06.720p.exe","size":2312863971,"progress":1,"priority":1}]`
`preferences.json`: `{"save_path":"/downloads","temp_path":"/downloads/incomplete/","temp_path_enabled":true,"max_ratio":1,"max_seeding_time":2880,"excluded_file_names":""}`

- [ ] **Step 2: Write failing tests**

```go
func newQB(t *testing.T, loginOK bool, seen *[]*http.Request) *httptest.Server {
	loggedIn := false
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r)
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if !loginOK { w.Write([]byte("Fails.")); return }
			loggedIn = true
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc"})
			w.Write([]byte("Ok."))
		case "/api/v2/app/version":
			if c, _ := r.Cookie("SID"); c == nil || !loggedIn { w.WriteHeader(403); return }
			w.Write([]byte("v5.1.4"))
		case "/api/v2/torrents/info":
			w.Write(fixture(t, "torrents_info.json"))
		case "/api/v2/torrents/files":
			w.Write(fixture(t, "torrent_files.json"))
		case "/api/v2/app/preferences":
			w.Write(fixture(t, "preferences.json"))
		default: // delete/reannounce/resume/setPreferences
			r.ParseForm(); w.WriteHeader(200)
		}
	}))
}
```
Tests: `TestLoginThenVersion` (login happens once, cookie reused: count `/auth/login` requests == 1 across two `Version` calls); `TestLoginFailureIsError` (body `Fails.` → error mentioning credentials); `TestTorrentsDecodeEpochAndDuration` (`CompletionOn` zero for `-1`, `SeedingTime == 48h`, `State == "stalledDL"`); `TestDeleteJoinsHashesAndFlags` (form `hashes=a|b`, `deleteFiles=true`); `TestNoCredsSkipsLogin`; `TestFakeRecordsCalls`.

- [ ] **Step 3: Run to fail.**

- [ ] **Step 4: Implement** — key parts:
```go
func New(baseURL, user, pass string, opts ...httpx.Option) (*HTTPClient, error) {
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	h, err := httpx.New(baseURL, append([]httpx.Option{httpx.WithHTTPClient(hc), httpx.WithHeader("Referer", baseURL)}, opts...)...)
	if err != nil { return nil, fmt.Errorf("qbittorrent: %w", err) }
	return &HTTPClient{h: h, user: user, pass: pass}, nil
}

func (c *HTTPClient) login(ctx context.Context) error {
	if c.user == "" && c.pass == "" { return nil }
	var body string
	if err := c.h.PostForm(ctx, "/api/v2/auth/login", url.Values{"username": {c.user}, "password": {c.pass}}, &body); err != nil {
		return fmt.Errorf("qbittorrent login: %w", err)
	}
	if strings.TrimSpace(body) != "Ok." { return errors.New("qbittorrent login: invalid credentials") }
	c.loggedIn = true
	return nil
}

// withAuth runs fn, logging in first if needed and once more after a 403.
func (c *HTTPClient) withAuth(ctx context.Context, fn func() error) error {
	if !c.loggedIn { if err := c.login(ctx); err != nil { return err } }
	err := fn()
	if httpx.IsStatus(err, http.StatusForbidden) {
		c.loggedIn = false
		if lerr := c.login(ctx); lerr != nil { return lerr }
		return fn()
	}
	return err
}
```
`epochTime.UnmarshalJSON`: integer ≤ 0 → zero time. `Torrents` builds query `filter`, `category` only when non-empty. `Delete` posts form `hashes`, `deleteFiles`. `SetPreferences` posts form `json=<encoded patch>`.

- [ ] **Step 5: Run tests** — PASS ≥ 80 %.

- [ ] **Step 6: Commit** — `git commit -m "feat(qbittorrent): v2 client with session auth, fake, fixtures"`

---

### Task 8: Plex, Tautulli and Overseerr clients

**Files:**
- Create: `internal/clients/plex/{types.go,client.go,fake.go,client_test.go}`, `internal/clients/plex/testdata/{identity.json,sections.json,recently_added.json}`
- Create: `internal/clients/tautulli/{types.go,client.go,fake.go,client_test.go}`, `internal/clients/tautulli/testdata/{history.json,activity.json}`
- Create: `internal/clients/overseerr/{types.go,client.go,fake.go,client_test.go}`, `internal/clients/overseerr/testdata/{status.json,requests.json}`

**Interfaces:**
```go
// plex
type Identity struct { MachineIdentifier, Version string }
type Library struct { Key, Title, Type string; ScannedAt, UpdatedAt time.Time }
type Item struct { RatingKey, Title, Type string; AddedAt time.Time; LibraryKey string }
type Client interface {
	Identity(ctx) (Identity, error)                 // GET /identity (no token needed)
	Libraries(ctx) ([]Library, error)               // GET /library/sections
	RecentlyAdded(ctx, libraryKey string, limit int) ([]Item, error) // GET /library/sections/{key}/recentlyAdded?X-Plex-Container-Size=
	RefreshLibrary(ctx, libraryKey string) error    // GET /library/sections/{key}/refresh
}
func New(baseURL, token string, opts ...httpx.Option) (*HTTPClient, error)   // sends X-Plex-Token + Accept: application/json
// tautulli
type HistoryRow struct { RatingKey, GrandparentRatingKey, ParentRatingKey string; Title, GrandparentTitle, MediaType, User string; Date time.Time; WatchedStatus float64; PercentComplete int }
type Client interface {
	Ping(ctx) error                                                 // cmd=status
	History(ctx, since time.Time, length int) ([]HistoryRow, error) // cmd=get_history&after=YYYY-MM-DD&length=
	ActivityCount(ctx) (int, error)                                 // cmd=get_activity → stream_count
}
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error)   // all calls GET /api/v2?apikey=&cmd=
// overseerr
type Status struct { Version string; Initialized bool }
type Request struct { ID int64; Status int; MediaType string; TMDBID, TVDBID int64; MediaStatus int; RequestedBy string; CreatedAt, UpdatedAt time.Time }
type Client interface {
	Status(ctx) (Status, error)                                   // GET /api/v1/status + /api/v1/settings/public for initialized
	Requests(ctx, filter string) ([]Request, error)               // GET /api/v1/request?filter=&take=100 paged via skip
	DeclineRequest(ctx, id int64) error                           // POST /api/v1/request/{id}/decline
}
func New(baseURL, apiKey string, opts ...httpx.Option) (*HTTPClient, error)   // X-Api-Key
```
- Plex JSON is wrapped: `{"MediaContainer": {"Directory": [...]}}` for sections, `{"MediaContainer": {"Metadata": [...]}}` for items; epoch ints for `scannedAt`, `updatedAt`, `addedAt`. Token also accepted as query `X-Plex-Token` — use header only.
- Plex over LAN: `New` is called with the `https://192-0-2-10.<hash>.plex.direct:32400` URL; TLS verification uses that hostname naturally, no `InsecureSkipVerify`.
- Tautulli returns `{"response": {"result": "success", "data": {...}}}`; `result != "success"` → error with `message`.
- Overseerr request `status`: 1 pending, 2 approved, 3 declined; `media.status`: 2 pending, 3 processing, 4 partially available, 5 available. `RequestedBy` = `requestedBy.email` or `displayName`.

- [ ] **Step 1: Capture fixtures** — Plex: from the Pi `curl -sk -H "Accept: application/json" "https://<plex.direct host>:32400/identity"`; sections and recentlyAdded need `X-Plex-Token` (read from `/volume1/docker/configs/plex/.../Preferences.xml` `PlexOnlineToken` on the NAS — copy into `secrets.toml`, never into git). Tautulli key: `Tautulli → Settings → Web Interface → API key`, or `grep api_key $DOCKER_CONFIG/tautulli/config.ini`. Overseerr key: `jq -r .main.apiKey $DOCKER_CONFIG/overseerr/settings.json`. Trim fixtures to ≤ 3 items with `jq`. Grep every fixture for `token|apikey|api_key|password` and redact.

- [ ] **Step 2: Write failing tests** — per package, with the `newServer`/`fixture` helpers (copy them; packages don't share test helpers). Assertions: plex `Libraries` decodes 2+ sections with non-zero `ScannedAt`; `RecentlyAdded` sends `X-Plex-Container-Size`; `Identity` works without token; header `X-Plex-Token` present on sections. tautulli `Ping` fails when `result:"error"`; `History` sends `after=` date and decodes `WatchedStatus`. overseerr `Requests` pages (`skip`) until `pageInfo.pages` reached; `DeclineRequest` POSTs `/api/v1/request/9/decline`. Each package: `TestFakeRecordsCalls`.

- [ ] **Step 3: Run to fail.** **Step 4: Implement** (types with private raw wrappers, epoch decoding shared by copy — three packages, three tiny `epochTime` types; acceptable duplication over a cross-package helper). **Step 5: Run tests** ≥ 80 %.

- [ ] **Step 6: Commit** — `git commit -m "feat(clients): plex, tautulli and overseerr clients with fakes"`

---

### Task 9: Docker engine client (unix socket)

**Files:**
- Create: `internal/clients/docker/{types.go,client.go,exec.go,fake.go,client_test.go}`, `internal/clients/docker/testdata/{containers.json,inspect.json,df.json}`

**Interfaces:**
```go
type Container struct { ID, Name, Image, State, Status string; Health string; RestartCount int; StartedAt time.Time }
type DiskUsage struct { ImagesTotal, ImagesActive int; ImagesSize, ImagesReclaimable int64; BuildCacheSize int64 }
type Client interface {
	Containers(ctx) ([]Container, error)            // GET /containers/json?all=1 then inspect each for health/restarts/StartedAt
	Logs(ctx, name string, tail int) (string, error) // GET /containers/{name}/logs?stdout=1&stderr=1&tail=; strips the 8-byte stream headers
	DiskUsage(ctx) (DiskUsage, error)               // GET /system/df
	PruneImages(ctx, dangling bool) (int64, error)  // POST /images/prune?filters={"dangling":["true"]}; returns SpaceReclaimed
	Exec(ctx, container string, args ...string) (string, error) // shells out to `<binary> exec <container> args...`
}
func New(socketPath, binary string) (*HTTPClient, error)
type Fake struct { List []Container; LogsByName map[string]string; Usage DiskUsage; ExecOut map[string]string; Calls []string; Err error }
```
- Transport: `http.Transport{DialContext: func(ctx, _, _ string) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "unix", socketPath) }}` with base URL `http://docker/v1.43`.
- `Exec` uses `os/exec` with the configured binary; tests stub it via an unexported `execCommand` variable.
- A permission-denied on the socket must return an error that says `add this user to the docker group` (NAS prerequisite), not a generic dial error.

- [ ] **Step 1: Fixtures** — from the Pi: `curl -s --unix-socket /var/run/docker.sock http://docker/v1.43/containers/json | jq '.[0:2]'`, `.../containers/sonarr/json`, `.../system/df | jq 'del(.Containers,.Volumes)'`.

- [ ] **Step 2: Failing tests** — use `httptest.NewUnstartedServer` + `net.Listen("unix", path)` in a `t.TempDir()` so the real socket dialer is exercised; assert `Containers` merges inspect data (`Health == "healthy"`, `RestartCount`), `Logs` strips stream headers (serve a body with the 8-byte prefix), `PruneImages` sends the dangling filter, `Exec` stub returns output, and a socket path that doesn't exist yields the docker-group hint only when `errors.Is(err, fs.ErrPermission)` (create an unwritable socket file with mode 0000 for that case; skip test if running as root).

- [ ] **Step 3: Run to fail. Step 4: Implement. Step 5: Tests PASS ≥ 80 %.**

- [ ] **Step 6: Commit** — `git commit -m "feat(docker): engine API over unix socket, exec shell-out, fake"`

---

### Task 10: Host filesystem probes (`hostfs`)

**Files:**
- Create: `internal/clients/hostfs/{types.go,mounts.go,disk.go,fake.go,mounts_test.go,disk_test.go}`, `internal/clients/hostfs/testdata/mountinfo.txt`

**Interfaces:**
```go
type Mount struct { Target, Source, FSType string; Options []string }
type Usage struct { Path string; Total, Free, Used uint64; UsedPercent float64 }
type Client interface {
	Mounts(ctx) ([]Mount, error)                     // parses /proc/self/mountinfo (path overridable for tests)
	IsMountpoint(ctx, path string) (bool, error)     // device of path != device of parent
	DeviceID(ctx, path string) (uint64, error)       // stat st_dev — compared with container-side `stat -c %d`
	Usage(ctx, path string) (Usage, error)           // syscall.Statfs
	DirSize(ctx, path string, maxDepth int) (int64, error) // walk, symlinks not followed
	ListDir(ctx, path string) ([]Entry, error)       // name, size, isDir, modTime
}
type Entry struct { Name string; Size int64; IsDir bool; ModTime time.Time }
func New(mountinfoPath string) *OS                 // "" → /proc/self/mountinfo
type Fake struct { MountList []Mount; Devices map[string]uint64; Usages map[string]Usage; Sizes map[string]int64; Entries map[string][]Entry; Calls []string; Err error }
```

- [ ] **Step 1: Fixture** — copy the Pi's `/proc/self/mountinfo` lines for `/`, `/mnt/nas/tv` (nfs) and one `autofs` line into `testdata/mountinfo.txt`.
- [ ] **Step 2: Failing tests** — `Mounts` parses target/fstype/options (handle the ` - ` separator and octal escapes like `\040`); `IsMountpoint` true for `t.TempDir()`'s filesystem root? — instead test with `/` (always a mountpoint) and a fresh temp dir (never); `Usage("/")` has `Total > 0`; `DirSize` on a temp tree with two files of known size; `ListDir` sorted by name.
- [ ] **Step 3–5:** fail → implement (`syscall.Stat_t.Dev`, `syscall.Statfs`) → PASS.
- [ ] **Step 6: Commit** — `git commit -m "feat(hostfs): mount, device, usage and directory probes"`

---

### Task 11: CLI verbs for every client + output formatting

**Files:**
- Create: `internal/cli/output.go`, `internal/cli/output_test.go`, `internal/cli/deps.go`, `internal/cli/cmd_sonarr.go`, `internal/cli/cmd_radarr.go`, `internal/cli/cmd_prowlarr.go`, `internal/cli/cmd_qbit.go`, `internal/cli/cmd_plex.go`, `internal/cli/cmd_tautulli.go`, `internal/cli/cmd_overseerr.go`, `internal/cli/cmd_docker.go`, `internal/cli/cmd_host.go`, `internal/cli/cmd_config.go`, `internal/cli/cmd_test.go`
- Modify: `internal/cli/root.go` (register subcommands; `Deps` gains fields), `cmd/healarr/main.go` (build `Deps` lazily from config)

**Interfaces:**
```go
type Deps struct {
	Load     func(path string) (config.Config, config.Secrets, error)   // default config.Load
	Sonarr   func(cfg config.Config, sec config.Secrets) (sonarr.Client, error)
	Radarr   func(...) (radarr.Client, error)   // …one constructor per client, same shape
	Prowlarr, QBit, Plex, Tautulli, Overseerr, Docker, Host  (same pattern)
}
func DefaultDeps() *Deps           // real constructors
func Print(w io.Writer, jsonMode bool, v any) error   // JSON (indented) or a plain-text table via text/tabwriter for slices of structs / single structs
```
Verbs (all read-only except where marked; writes honour `--dry-run` by printing the intended call and returning):
- `sonarr health|queue|series|rootfolders|missing|history [--since 24h] [--type]|queue-delete <id> [--remove-from-client] [--blocklist] (W)|monitor <id> on|off (W)|series-delete <id> [--delete-files] (W)|command <name> (W)`
- `radarr …` same set with `movies`/`movie-delete`
- `prowlarr health|indexers|status|indexer-delete <id> (W)|indexer-test <id>`
- `qbit version|list [--filter] [--category]|files <hash>|delete <hash…> [--files] (W)|reannounce <hash…> (W)|resume <hash…> (W)|prefs`
- `plex identity|libraries|recent <key> [--limit]|refresh <key> (W)`
- `tautulli ping|history [--since 30d] [--length]|activity`
- `overseerr status|requests [--filter]|decline <id> (W)`
- `docker ps|logs <name> [--tail]|df|prune [--dangling] (W)|exec <name> -- <args…>`
- `host mounts|ismount <path>|dev <path>|usage <path>|dirsize <path> [--depth]|ls <path>`
- `config validate` (loads config+secrets, prints node, service URLs, which keys are present — never the values)

- [ ] **Step 1: Failing tests** — `output_test.go`: `Print` with `jsonMode=true` emits valid JSON; with a slice of structs emits a header row from field names. `cmd_test.go`: build root with `Deps` whose constructors return fakes; run `sonarr missing --json` → `{"missing": 3}`; run `sonarr queue-delete 7 --blocklist --dry-run` → no call recorded on the fake and output contains `would DeleteQueueItem(7`; run `qbit delete a b --files` → fake `Calls[0] == "Delete([a b],true)"`; `config validate` with a temp config prints `node: pi` and `sonarr_api_key: set`.
- [ ] **Step 2: Run to fail. Step 3: Implement** — each `cmd_*.go` ≤ 150 lines: a `newSonarrCmd(deps *Deps, flags *GlobalFlags) *cobra.Command` that lazily builds the client inside `RunE` (so `version` never touches config). Shared helper `func (d *Deps) load(flags *GlobalFlags) (config.Config, config.Secrets, error)`. `--since` parses Go durations plus `Nd` days suffix (`parseSince("30d")`).
- [ ] **Step 4: Tests PASS.** Also run the binary against the live Pi as a smoke check: `make build-pi && scp dist/healarr-linux-arm64 pi:/tmp/healarr && ssh pi '/tmp/healarr --config /tmp/healarr.toml sonarr health --json'` after writing a throwaway `/tmp/healarr.toml` + `/tmp/secrets.toml` (0600) on the Pi pointing at the localhost services with `api_key_file` paths.
- [ ] **Step 5: Commit** — `git commit -m "feat(cli): verbs for every service client, json/table output, dry-run"`

---

### Task 12: Docs — ADRs, README, runbook; drop stale Python docs

**Files:**
- Modify: `docs/decisions.md` (append ADR-013..016; mark ADR-002 and ADR-005 superseded), `README.md` (rewrite: what it is, Phase status, build, config, CLI examples), `docs/runbook.md` (first-run for Pi + NAS with the binary, config paths, `config validate`), `docs/architecture.md` (replace the Python component list with the Go package layout from the spec; keep goals/non-goals/tiers/worked example)
- Delete: nothing else (tools.md stays as the future tool inventory).

- [ ] **Step 1: Write ADRs** — each with Context / Decision / Consequences:
  - **ADR-013 — Go single binary (supersedes ADR-002):** NAS has Python 3.8 only and no docker socket for the agent user; a `CGO_ENABLED=0` binary needs nothing installed; `modernc.org/sqlite` keeps SQLite (ADR-003).
  - **ADR-014 — LAN web approvals, no IMAP (supersedes ADR-005):** Pi already runs nginx; a `/healarr/` page behind it avoids a second mailbox credential and reply parsing; trade-off: decisions only from the LAN/VPN.
  - **ADR-015 — Two nodes, Pi primary:** NAS runs its own agent for qBit/Plex/disk checks; HTTP-over-LAN peer channel with bearer token; NFS-shared state rejected because the NFS mount is itself a thing being monitored.
  - **ADR-016 — Deterministic staleness score with human decision:** formula from the spec (§C6), weights in config, delete executes through Sonarr/Radarr so state stays consistent; never auto-deletes library media.
- [ ] **Step 2: README** — replace Quickstart with `make build-pi`/`build-nas`, config/secrets locations, `healarr config validate`, three CLI examples, phase table (Phase 1 ✅ after this task).
- [ ] **Step 3: Commit** — `git add docs README.md && git commit -m "docs: ADR-013..016, Go README and runbook"`

---

## Self-review notes

- Spec coverage for Phase 1: CLI surface (§C2 CLI list) → Task 11; libraries (§C1) → Tasks 1–3 (cobra, toml) — `golift.io/starr` and `go-qbittorrent` were **not** adopted: the raw endpoints we need are few and fixture-tested directly, which keeps the dependency graph to two modules; note this in ADR-013. Config/secrets/node identity → Task 2. Cross-compile + CI → Task 1. Fixtures from the live stack (§Testing) → Steps 1 of Tasks 4–10. ADRs → Task 12.
- Not in Phase 1 (by design): store, checks, peer, agent, web, LLM — Phases 2–5 get their own plans once these clients exist.
- Type consistency: every `Fake` exposes `Calls []string` and `Err error`; every `New` returns `(*HTTPClient, error)` and accepts `...httpx.Option`; `Client` interfaces take `context.Context` first everywhere.
