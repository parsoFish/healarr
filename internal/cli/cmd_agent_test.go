package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/agent"
	"github.com/parsoFish/healarr/internal/config"
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
		return agentServeTestCfg(config.NodePi), config.Secrets{PeerToken: "tok123"}, nil
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

// TestAgentServeNewAgentFailureStillClosesStore proves a failure building
// the daemon itself still closes the store that was already opened (the
// error-join path), rather than leaking the connection.
func TestAgentServeNewAgentFailureStillClosesStore(t *testing.T) {
	deps, fk, _ := newPhase3Deps(t)
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return agentServeTestCfg(config.NodePi), config.Secrets{}, nil
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
