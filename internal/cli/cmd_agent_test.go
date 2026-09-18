package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/agent"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/decision"
	"github.com/parsoFish/healarr/internal/store"
	"github.com/parsoFish/healarr/internal/version"
)

// agentServeTestCfg mirrors internal/agent/run_test.go's runTestCfg: every
// field Agent.Schedule/Run reads set to a value that parses cleanly, so the
// daemon actually starts (and Schedule doesn't error) instead of merely
// exercising the CLI wiring around a Schedule failure.
func agentServeTestCfg(node config.Node) config.Config {
	return config.Config{
		Node:   node,
		Checks: config.Checks{Timeout: testCheckTimeout},
		Agent: config.Agent{
			HeartbeatInterval: 5 * time.Minute,
			CheckpointAt:      "03:00",
			PeerStaleAfter:    15 * time.Minute,
		},
		Email: config.Email{DigestAt: "07:00"},
	}
}

func TestAgentServeDryRunErrors(t *testing.T) {
	deps, _ := newTestDeps()
	_, err := runCLIErr(t, deps, "--dry-run", "agent", "serve")
	if err == nil || !strings.Contains(err.Error(), "does not support --dry-run") {
		t.Fatalf("err = %v, want an error naming --dry-run unsupported", err)
	}
}

// TestAgentServeBuildsAgentRunsAndClosesStore proves the happy path wires
// agent.Options correctly (PeerToken/Version/Sender), opens the store
// before Run and closes it after, and returns promptly once ctx is
// already cancelled — exactly like internal/agent's own Run tests.
func TestAgentServeBuildsAgentRunsAndClosesStore(t *testing.T) {
	deps, fk, p3 := newPhase3Deps(t)
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return agentServeTestCfg(config.NodePi), config.Secrets{PeerToken: "tok123", WebToken: "webtok"}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := runCLICtx(t, ctx, deps, "agent", "serve"); err != nil {
		t.Fatalf("agent serve err = %v, want nil", err)
	}

	if !fk.Store.Opened {
		t.Error("expected agent serve to open the store")
	}
	if !fk.Store.Closed {
		t.Error("expected agent serve to close the store")
	}
	if len(p3.AgentOptions) != 1 {
		t.Fatalf("NewAgent called %d times, want 1", len(p3.AgentOptions))
	}
	opts := p3.AgentOptions[0]
	if opts.PeerToken != "tok123" {
		t.Errorf("Options.PeerToken = %q, want %q", opts.PeerToken, "tok123")
	}
	if opts.Version != version.Version {
		t.Errorf("Options.Version = %q, want %q", opts.Version, version.Version)
	}
	if opts.Sender != p3.Sender {
		t.Errorf("Options.Sender = %v, want the fake sender", opts.Sender)
	}
	if opts.Web == nil {
		t.Error("Options.Web = nil, want the pi's web handler")
	}
	if opts.WebAddr != agentServeTestCfg(config.NodePi).Web.ListenAddr {
		t.Errorf("Options.WebAddr = %q, want %q", opts.WebAddr, agentServeTestCfg(config.NodePi).Web.ListenAddr)
	}
}

// TestAgentServeNASNeverWiresWebHandler proves the nas node never builds
// a web handler (Options.Web/WebAddr stay nil/"") even with no web_token
// at all — the web UI is pi-only, so a missing token there is simply
// irrelevant, not an error.
func TestAgentServeNASNeverWiresWebHandler(t *testing.T) {
	deps, _, p3 := newPhase3Deps(t)
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return agentServeTestCfg(config.NodeNAS), config.Secrets{}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := runCLICtx(t, ctx, deps, "agent", "serve"); err != nil {
		t.Fatalf("agent serve err = %v, want nil", err)
	}

	if len(p3.AgentOptions) != 1 {
		t.Fatalf("NewAgent called %d times, want 1", len(p3.AgentOptions))
	}
	opts := p3.AgentOptions[0]
	if opts.Web != nil {
		t.Errorf("Options.Web = %v, want nil on the nas", opts.Web)
	}
	if opts.WebAddr != "" {
		t.Errorf("Options.WebAddr = %q, want \"\" on the nas", opts.WebAddr)
	}
}

// TestAgentServePiWithoutWebTokenErrors proves a pi with no web_token
// configured refuses to start at all, rather than silently serving the
// daemon with no web UI (see errMissingWebToken).
func TestAgentServePiWithoutWebTokenErrors(t *testing.T) {
	deps, fk, p3 := newPhase3Deps(t)
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return agentServeTestCfg(config.NodePi), config.Secrets{}, nil
	}

	_, err := runCLIErr(t, deps, "agent", "serve")
	if !errors.Is(err, errMissingWebToken) {
		t.Fatalf("err = %v, want wrapping errMissingWebToken", err)
	}
	if len(p3.AgentOptions) != 0 {
		t.Errorf("NewAgent called %d times, want 0 (refused before building the daemon)", len(p3.AgentOptions))
	}
	if !fk.Store.Opened || !fk.Store.Closed {
		t.Errorf("Opened=%v Closed=%v, want both true (the store closes even on this early error)", fk.Store.Opened, fk.Store.Closed)
	}
}

// TestAgentServeStoreOpenFailurePropagates proves a store-open failure
// surfaces from the command instead of being swallowed, and that the
// daemon is never built when the store never opened.
func TestAgentServeStoreOpenFailurePropagates(t *testing.T) {
	deps, _, p3 := newPhase3Deps(t)
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return agentServeTestCfg(config.NodePi), config.Secrets{}, nil
	}
	boom := errors.New("boom")
	deps.OpenAgentStore = func(context.Context, config.Config) (AgentStoreCloser, error) { return nil, boom }

	if _, err := runCLIErr(t, deps, "agent", "serve"); err == nil || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
	if len(p3.AgentOptions) != 0 {
		t.Errorf("NewAgent called %d times, want 0 (store never opened)", len(p3.AgentOptions))
	}
}

// TestDecisionRunnerAdapterExecuteRunsKeepEndToEnd proves the adapter
// builds a fresh check.Deps per call (buildWebHandler never caches it)
// and hands it, along with its own Store/Peer, through to
// decision.Execute — exercised via the "keep" path, which needs no
// Sonarr/Radarr/Overseerr client at all.
func TestDecisionRunnerAdapterExecuteRunsKeepEndToEnd(t *testing.T) {
	deps, _ := newTestDeps()
	fk := &FakeStore{
		DecisionsByID: map[int64]store.Decision{
			7: {ID: 7, EntityKey: "sonarr:1", Kind: string(decision.KindKeep), Status: "pending"},
		},
	}
	cfg := config.Config{Node: config.NodePi, Staleness: config.Staleness{SnoozeDays: 10}}
	runner := &decisionRunnerAdapter{
		deps:  deps,
		flags: &GlobalFlags{},
		cfg:   cfg,
		sec:   config.Secrets{},
		store: fk,
	}

	got, err := runner.Execute(context.Background(), 7)
	if err != nil {
		t.Fatalf("Execute() err = %v", err)
	}
	if got.Status != "executed" {
		t.Fatalf("Status = %q, want executed", got.Status)
	}
	if len(fk.SnoozeEntityCalls) != 1 || fk.SnoozeEntityCalls[0].EntityKey != "sonarr:1" {
		t.Fatalf("SnoozeEntityCalls = %+v, want one call for sonarr:1", fk.SnoozeEntityCalls)
	}
	if len(fk.MarkDecisionCalls) != 1 || fk.MarkDecisionCalls[0].Status != "executed" {
		t.Fatalf("MarkDecisionCalls = %+v, want one executed call", fk.MarkDecisionCalls)
	}
}

// TestAgentServeNewAgentFailureStillClosesStore proves a failure building
// the daemon itself still closes the store that was already opened (the
// error-join path), rather than leaking the connection.
func TestAgentServeNewAgentFailureStillClosesStore(t *testing.T) {
	deps, fk, _ := newPhase3Deps(t)
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return agentServeTestCfg(config.NodePi), config.Secrets{WebToken: "webtok"}, nil
	}
	boom := errors.New("boom")
	deps.NewAgent = func(agent.Options) (*agent.Agent, error) { return nil, boom }

	_, err := runCLIErr(t, deps, "agent", "serve")
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
	if !fk.Store.Opened || !fk.Store.Closed {
		t.Errorf("Opened=%v Closed=%v, want both true", fk.Store.Opened, fk.Store.Closed)
	}
}
