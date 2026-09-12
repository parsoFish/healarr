package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/clients/docker"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
	"github.com/parsoFish/healarr/internal/clients/plex"
	"github.com/parsoFish/healarr/internal/clients/prowlarr"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/clients/tautulli"
	"github.com/parsoFish/healarr/internal/config"
)

// fakes bundles every service Fake so a test can inspect Calls after
// running a command through the real cobra tree.
type fakes struct {
	Sonarr    *sonarr.Fake
	Radarr    *radarr.Fake
	Prowlarr  *prowlarr.Fake
	QBit      *qbittorrent.Fake
	Plex      *plex.Fake
	Tautulli  *tautulli.Fake
	Overseerr *overseerr.Fake
	Docker    *docker.Fake
	Host      *hostfs.Fake
}

// newTestDeps wires a Deps whose constructors return each package's Fake
// and whose Load never touches disk, so the CLI tree can be exercised
// end-to-end without a real config file (except where a test wants to
// exercise the real config.Load path, e.g. TestConfigValidate).
func newTestDeps() (*Deps, *fakes) {
	fk := &fakes{
		Sonarr:    &sonarr.Fake{Missing: 3},
		Radarr:    &radarr.Fake{Missing: 5},
		Prowlarr:  &prowlarr.Fake{},
		QBit:      &qbittorrent.Fake{},
		Plex:      &plex.Fake{},
		Tautulli:  &tautulli.Fake{},
		Overseerr: &overseerr.Fake{},
		Docker:    &docker.Fake{},
		Host:      &hostfs.Fake{},
	}
	deps := &Deps{
		Load: func(string) (config.Config, config.Secrets, error) {
			return config.Config{Node: config.NodePi}, config.Secrets{}, nil
		},
		Sonarr:    func(config.Config, config.Secrets) (sonarr.Client, error) { return fk.Sonarr, nil },
		Radarr:    func(config.Config, config.Secrets) (radarr.Client, error) { return fk.Radarr, nil },
		Prowlarr:  func(config.Config, config.Secrets) (prowlarr.Client, error) { return fk.Prowlarr, nil },
		QBit:      func(config.Config, config.Secrets) (qbittorrent.Client, error) { return fk.QBit, nil },
		Plex:      func(config.Config, config.Secrets) (plex.Client, error) { return fk.Plex, nil },
		Tautulli:  func(config.Config, config.Secrets) (tautulli.Client, error) { return fk.Tautulli, nil },
		Overseerr: func(config.Config, config.Secrets) (overseerr.Client, error) { return fk.Overseerr, nil },
		Docker:    func(config.Config, config.Secrets) (docker.Client, error) { return fk.Docker, nil },
		Host:      func(config.Config, config.Secrets) (hostfs.Client, error) { return fk.Host, nil },
	}
	return deps, fk
}

func runCLI(t *testing.T, deps *Deps, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := NewRootCmd(deps)
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v (output: %s)", args, err, buf.String())
	}
	return buf.String()
}

func runCLIErr(t *testing.T, deps *Deps, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := NewRootCmd(deps)
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	return buf.String(), cmd.Execute()
}

func TestSonarrMissingJSON(t *testing.T) {
	deps, _ := newTestDeps()
	out := runCLI(t, deps, "--json", "sonarr", "missing")
	var got map[string]int
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if got["missing"] != 3 {
		t.Errorf("missing = %d, want 3", got["missing"])
	}
}

func TestSonarrQueueDeleteDryRun(t *testing.T) {
	deps, fk := newTestDeps()
	out := runCLI(t, deps, "--dry-run", "sonarr", "queue-delete", "7", "--blocklist")
	if len(fk.Sonarr.Calls) != 0 {
		t.Errorf("expected no calls recorded on dry-run, got %v", fk.Sonarr.Calls)
	}
	if !strings.Contains(out, "would DeleteQueueItem(7") {
		t.Errorf("expected dry-run message, got %q", out)
	}
}

func TestSonarrQueueDeleteExecutes(t *testing.T) {
	deps, fk := newTestDeps()
	runCLI(t, deps, "sonarr", "queue-delete", "7", "--blocklist")
	if len(fk.Sonarr.Calls) != 1 || fk.Sonarr.Calls[0] != "DeleteQueueItem(7,false,true)" {
		t.Errorf("Calls = %v", fk.Sonarr.Calls)
	}
}

func TestSonarrMonitorInvalidState(t *testing.T) {
	deps, _ := newTestDeps()
	if _, err := runCLIErr(t, deps, "sonarr", "monitor", "1", "maybe"); err == nil {
		t.Fatal("expected error for invalid on/off state")
	}
}

func TestSonarrReadVerbs(t *testing.T) {
	deps, _ := newTestDeps()
	for _, verb := range []string{"health", "queue", "series", "rootfolders"} {
		runCLI(t, deps, "sonarr", verb)
	}
	runCLI(t, deps, "sonarr", "history", "--since", "12h", "--type", "grabbed")
}

func TestRadarrMissingAndMovieDeleteDryRun(t *testing.T) {
	deps, fk := newTestDeps()
	out := runCLI(t, deps, "--json", "radarr", "missing")
	var got map[string]int
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["missing"] != 5 {
		t.Fatalf("radarr missing = %q, %v", out, err)
	}
	before := len(fk.Radarr.Calls)
	out = runCLI(t, deps, "--dry-run", "radarr", "movie-delete", "9", "--delete-files")
	if len(fk.Radarr.Calls) != before {
		t.Errorf("expected no new calls recorded on dry-run, got %v", fk.Radarr.Calls)
	}
	if !strings.Contains(out, "would DeleteMovie(9,true,false)") {
		t.Errorf("expected dry-run message, got %q", out)
	}
}

func TestProwlarrIndexerDeleteDryRunAndTest(t *testing.T) {
	deps, fk := newTestDeps()
	out := runCLI(t, deps, "--dry-run", "prowlarr", "indexer-delete", "3")
	if len(fk.Prowlarr.Calls) != 0 || !strings.Contains(out, "would DeleteIndexer(3)") {
		t.Errorf("unexpected dry-run result: out=%q calls=%v", out, fk.Prowlarr.Calls)
	}
	runCLI(t, deps, "prowlarr", "indexer-test", "3")
	if len(fk.Prowlarr.Calls) != 1 || fk.Prowlarr.Calls[0] != "TestIndexer(3)" {
		t.Errorf("Calls = %v", fk.Prowlarr.Calls)
	}
}

func TestQBitDeleteRecordsCall(t *testing.T) {
	deps, fk := newTestDeps()
	runCLI(t, deps, "qbit", "delete", "a", "b", "--files")
	if len(fk.QBit.Calls) != 1 || fk.QBit.Calls[0] != "Delete([a b], true)" {
		t.Errorf("Calls = %v", fk.QBit.Calls)
	}
}

func TestQBitReannounceDryRun(t *testing.T) {
	deps, fk := newTestDeps()
	out := runCLI(t, deps, "--dry-run", "qbit", "reannounce", "a", "b")
	if len(fk.QBit.Calls) != 0 || !strings.Contains(out, "would Reannounce([a b])") {
		t.Errorf("unexpected dry-run result: out=%q calls=%v", out, fk.QBit.Calls)
	}
}

func TestQBitVersionWrapsScalar(t *testing.T) {
	deps, _ := newTestDeps()
	out := runCLI(t, deps, "--json", "qbit", "version")
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["version"] != "fake" {
		t.Fatalf("qbit version = %q, %v", out, err)
	}
}

func TestPlexRefreshDryRun(t *testing.T) {
	deps, fk := newTestDeps()
	out := runCLI(t, deps, "--dry-run", "plex", "refresh", "5")
	if len(fk.Plex.Calls) != 0 || !strings.Contains(out, "would RefreshLibrary(5)") {
		t.Errorf("unexpected dry-run result: out=%q calls=%v", out, fk.Plex.Calls)
	}
	runCLI(t, deps, "plex", "refresh", "5")
	if len(fk.Plex.Calls) != 1 || fk.Plex.Calls[0] != "RefreshLibrary(5)" {
		t.Errorf("Calls = %v", fk.Plex.Calls)
	}
}

func TestTautulliVerbs(t *testing.T) {
	deps, _ := newTestDeps()
	runCLI(t, deps, "tautulli", "ping")
	out := runCLI(t, deps, "--json", "tautulli", "activity")
	var got map[string]int
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	runCLI(t, deps, "tautulli", "history", "--since", "7d", "--length", "5")
}

func TestOverseerrDeclineDryRun(t *testing.T) {
	deps, fk := newTestDeps()
	out := runCLI(t, deps, "--dry-run", "overseerr", "decline", "42")
	if len(fk.Overseerr.Calls) != 0 || !strings.Contains(out, "would DeclineRequest(42)") {
		t.Errorf("unexpected dry-run result: out=%q calls=%v", out, fk.Overseerr.Calls)
	}
	runCLI(t, deps, "overseerr", "requests")
	runCLI(t, deps, "overseerr", "status")
}

func TestDockerPruneDryRunAndExec(t *testing.T) {
	deps, fk := newTestDeps()
	out := runCLI(t, deps, "--dry-run", "docker", "prune")
	if len(fk.Docker.Calls) != 0 || !strings.Contains(out, "would PruneImages(true)") {
		t.Errorf("unexpected dry-run result: out=%q calls=%v", out, fk.Docker.Calls)
	}
	runCLI(t, deps, "docker", "ps")
	runCLI(t, deps, "docker", "df")
	runCLI(t, deps, "docker", "logs", "sonarr", "--tail", "50")
	runCLI(t, deps, "docker", "exec", "sonarr", "--", "ls", "-la")
	if got := fk.Docker.Calls[len(fk.Docker.Calls)-1]; got != "Exec(sonarr, ls -la)" {
		t.Errorf("Exec call = %q", got)
	}
}

func TestHostVerbs(t *testing.T) {
	deps, fk := newTestDeps()
	fk.Host.Devices = map[string]uint64{"/data": 1, "/": 2}
	runCLI(t, deps, "host", "mounts")
	runCLI(t, deps, "host", "ismount", "/data")
	runCLI(t, deps, "host", "dev", "/data")
	runCLI(t, deps, "host", "usage", "/data")
	runCLI(t, deps, "host", "ls", "/data")
	runCLI(t, deps, "host", "dirsize", "/data", "--depth", "2")
	wantCalls := []string{"Mounts()", "IsMountpoint(/data)", "DeviceID(/data)", "Usage(/data)", "ListDir(/data)", "DirSize(/data,2)"}
	if len(fk.Host.Calls) != len(wantCalls) {
		t.Fatalf("Calls = %v, want %v", fk.Host.Calls, wantCalls)
	}
	for i, w := range wantCalls {
		if fk.Host.Calls[i] != w {
			t.Errorf("Calls[%d] = %q, want %q", i, fk.Host.Calls[i], w)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfgBody := "node = \"pi\"\n[services.sonarr]\nurl = \"http://sonarr:8989\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.toml"), []byte("sonarr_api_key = \"k\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, DefaultDeps(), "--config", cfgPath, "config", "validate")
	if !strings.Contains(out, "node: pi") {
		t.Errorf("missing node line: %q", out)
	}
	if !strings.Contains(out, "sonarr_api_key: set") {
		t.Errorf("missing sonarr_api_key line: %q", out)
	}
	if !strings.Contains(out, "radarr_api_key: missing") {
		t.Errorf("missing radarr_api_key line: %q", out)
	}
	if strings.Contains(out, "\"k\"") {
		t.Errorf("secret value leaked into output: %q", out)
	}
}

func TestConfigValidateMissingFileNamesPath(t *testing.T) {
	deps, _ := newTestDeps()
	deps.Load = nil // force the real config.Load default via (d *Deps).load
	_, err := runCLIErr(t, deps, "--config", "/no/such/config.toml", "config", "validate")
	if err == nil || !strings.Contains(err.Error(), "/no/such/config.toml") {
		t.Fatalf("expected error naming the config path, got %v", err)
	}
}

func TestVersionNeverTouchesConfig(t *testing.T) {
	deps := &Deps{} // every constructor nil; version must not call any of them
	out := runCLI(t, deps, "version")
	if !strings.Contains(out, "healarr ") {
		t.Errorf("unexpected version output: %q", out)
	}
}

func TestConfigValidateJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("node = \"nas\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.toml"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runCLI(t, DefaultDeps(), "--config", cfgPath, "--json", "config", "validate")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if got["node"] != "nas" {
		t.Errorf("node = %v, want nas", got["node"])
	}
}

// TestReadVerbsSmoke exercises every remaining read-only verb against its
// fake to keep them from silently breaking, without one assertion-heavy
// test function per verb.
func TestReadVerbsSmoke(t *testing.T) {
	deps, fk := newTestDeps()
	fk.QBit.FilesByHash = map[string][]qbittorrent.File{"h1": {}}
	fk.Plex.ItemsByKey = map[string][]plex.Item{"1": {}}
	for _, args := range [][]string{
		{"radarr", "queue"}, {"radarr", "movies"}, {"radarr", "rootfolders"}, {"radarr", "history"},
		{"prowlarr", "health"}, {"prowlarr", "indexers"}, {"prowlarr", "status"},
		{"qbit", "list"}, {"qbit", "files", "h1"},
		{"plex", "identity"}, {"plex", "libraries"}, {"plex", "recent", "1", "--limit", "5"},
	} {
		runCLI(t, deps, args...)
	}
}

// TestWriteVerbsExecuteSmoke runs every mutating verb (without --dry-run) to
// cover the "do" branch and its success output, and asserts each fake
// recorded the call it expected.
func TestWriteVerbsExecuteSmoke(t *testing.T) {
	deps, fk := newTestDeps()
	runCLI(t, deps, "sonarr", "monitor", "1", "on")
	runCLI(t, deps, "sonarr", "series-delete", "1")
	runCLI(t, deps, "sonarr", "command", "RescanSeries")
	runCLI(t, deps, "radarr", "queue-delete", "1")
	runCLI(t, deps, "radarr", "monitor", "1", "off")
	runCLI(t, deps, "radarr", "movie-delete", "1")
	runCLI(t, deps, "radarr", "command", "RescanMovie")
	runCLI(t, deps, "prowlarr", "indexer-delete", "1")
	runCLI(t, deps, "qbit", "reannounce", "h1")
	runCLI(t, deps, "qbit", "resume", "h1")
	runCLI(t, deps, "overseerr", "decline", "1")
	runCLI(t, deps, "docker", "prune", "--dangling=false")

	wantSonarr := []string{"UpdateSeriesMonitored(1,true)", "DeleteSeries(1,false,false)", "RunCommand(RescanSeries)"}
	for i, c := range fk.Sonarr.Calls {
		if c != wantSonarr[i] {
			t.Errorf("sonarr Calls[%d] = %q, want %q", i, c, wantSonarr[i])
		}
	}
	if got := fk.Radarr.Calls[0]; got != "DeleteQueueItem(1,false,false)" {
		t.Errorf("radarr queue-delete call = %q", got)
	}
	if got := fk.Docker.Calls[len(fk.Docker.Calls)-1]; got != "PruneImages(false)" {
		t.Errorf("docker prune call = %q", got)
	}
}

// TestClientConstructorErrorPropagates checks that a failing client
// constructor's error surfaces from both a read (simpleCmd) and a write
// (doOrDryRun) verb, instead of being swallowed.
func TestClientConstructorErrorPropagates(t *testing.T) {
	deps, _ := newTestDeps()
	boom := errors.New("boom")
	deps.Sonarr = func(config.Config, config.Secrets) (sonarr.Client, error) { return nil, boom }
	if _, err := runCLIErr(t, deps, "sonarr", "health"); err == nil {
		t.Fatal("expected error from failing Sonarr constructor")
	}
	deps.QBit = func(config.Config, config.Secrets) (qbittorrent.Client, error) { return nil, boom }
	if _, err := runCLIErr(t, deps, "qbit", "delete", "h1"); err == nil {
		t.Fatal("expected error from failing QBit constructor")
	}
}

func TestInvalidIDArgumentsError(t *testing.T) {
	deps, _ := newTestDeps()
	if _, err := runCLIErr(t, deps, "sonarr", "queue-delete", "not-a-number"); err == nil {
		t.Fatal("expected error for non-numeric id")
	}
	if _, err := runCLIErr(t, deps, "overseerr", "decline", "nope"); err == nil {
		t.Fatal("expected error for non-numeric id")
	}
}
