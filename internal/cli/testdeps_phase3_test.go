package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/agent"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/notify"
	"github.com/parsoFish/healarr/internal/peer"
)

// phase3TestRecipient is the email.to every Phase 3 CLI test's config
// carries, so a test asserting on the address sent to names the same
// constant the config does.
const phase3TestRecipient = "ops@example.test"

// phase3Fakes bundles the Phase 3 fakes (peer, sender, and every Options
// NewAgent was called with) that newPhase3Deps wires on top of the fakes
// newTestDeps already builds.
type phase3Fakes struct {
	Peer   *peer.Fake
	Sender *notify.FakeSender

	// AgentOptions records every agent.Options a test's `agent serve` run
	// built, in call order, so a test can assert on the exact wiring
	// (PeerToken, Version, Sender, ...) without inspecting the private
	// *agent.Agent it produced.
	AgentOptions []agent.Options
}

// newPhase3Deps extends newTestDeps with the Phase 3 Deps fields
// (PeerClient, Sender, OpenAgentStore, NewAgent) so `agent serve`,
// `notify test` and `peer ping` can be exercised through the real cobra
// tree without touching disk, a real MTA or a real peer. OpenAgentStore
// reuses fk.Store — the same FakeStore newTestDeps wires for StoreAPI —
// since FakeStore also satisfies AgentStoreCloser.
func newPhase3Deps(t *testing.T) (*Deps, *fakes, *phase3Fakes) {
	t.Helper()
	deps, fk := newTestDeps()
	p3 := &phase3Fakes{Peer: &peer.Fake{}, Sender: &notify.FakeSender{}}

	// A Pi with mail wired up: `notify test` resolves its recipient from
	// email.to unless --to overrides it, so the default config a Phase 3
	// test runs against has one.
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodePi, Email: config.Email{To: phase3TestRecipient}}, config.Secrets{}, nil
	}

	deps.PeerClient = func(config.Config, config.Secrets) (peer.Client, error) { return p3.Peer, nil }
	deps.Sender = func(config.Config) notify.Sender { return p3.Sender }
	deps.OpenAgentStore = func(context.Context, config.Config) (AgentStoreCloser, error) {
		fk.Store.Opened = true
		return fk.Store, nil
	}
	deps.NewAgent = func(o agent.Options) (*agent.Agent, error) {
		p3.AgentOptions = append(p3.AgentOptions, o)
		return agent.New(o)
	}
	return deps, fk, p3
}

// runCLICtx runs the cobra tree with ctx as the command's context (rather
// than the background context runCLI/runCLIErr always use), in its own
// goroutine, failing the test if it doesn't return within 2s. Only
// `agent serve` needs this: it blocks on ctx until a shutdown signal, so
// tests drive it with an already-cancelled context and expect a prompt
// return, exactly as internal/agent's own run_test.go does for Agent.Run.
func runCLICtx(t *testing.T, ctx context.Context, deps *Deps, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := NewRootCmd(deps)
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)

	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	select {
	case err := <-done:
		return buf.String(), err
	case <-time.After(2 * time.Second):
		t.Fatal("command did not return within 2s")
		return "", nil // unreachable; t.Fatal stops the goroutine
	}
}
