package agent

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

var runT0 = time.Date(2026, 9, 19, 12, 3, 0, 0, time.UTC)

// runTestCfg is a Config with every field Schedule/Run reads set to a
// valid value; ListenAddr is empty by default (no peer server) since most
// Run tests don't need one.
func runTestCfg(node config.Node) config.Config {
	return config.Config{
		Node:   node,
		Checks: config.Checks{Timeout: 5 * time.Second},
		Agent: config.Agent{
			HeartbeatInterval: 5 * time.Minute,
			CheckpointAt:      "03:00",
			PeerStaleAfter:    15 * time.Minute,
		},
		Email: config.Email{DigestAt: "07:00", To: "ops@example.test"},
	}
}

func newRunTestAgent(t *testing.T, mutate func(*Options)) *Agent {
	t.Helper()
	o := Options{
		Cfg:      runTestCfg(config.NodePi),
		Registry: check.NewRegistry(),
		Deps:     fakeDeps(check.Deps{Node: config.NodePi, Now: func() time.Time { return runT0 }}, nil),
		Store:    &fakeStore{},
		Now:      fixedNow(runT0),
		Version:  "v-test",
		Logger:   slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	}
	if mutate != nil {
		mutate(&o)
	}
	a, err := New(o)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	return a
}

// runWithTimeout runs a.Run(ctx) in its own goroutine and waits up to 2s
// for it to return, failing the test if it doesn't — Run must never hang
// once ctx is (or becomes) done.
func runWithTimeout(t *testing.T, a *Agent, ctx context.Context) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return within 2s")
		return nil // unreachable; t.Fatal stops the goroutine
	}
}

// TestRunWithCancelledContextReturnsPromptly proves Run's graceful
// shutdown path: an already-cancelled context makes it return quickly,
// with the peer server (bound to 127.0.0.1:0, an ephemeral port) shut
// down cleanly and no error.
func TestRunWithCancelledContextReturnsPromptly(t *testing.T) {
	a := newRunTestAgent(t, func(o *Options) {
		o.Cfg.Peer.ListenAddr = "127.0.0.1:0"
		o.PeerToken = "tok"
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runWithTimeout(t, a, ctx); err != nil {
		t.Fatalf("Run() err = %v, want nil", err)
	}
}

// TestRunWithoutPeerListenerSkipsServer proves Run works with no peer
// listener configured at all (cfg.Peer.ListenAddr == ""): no token is
// required, and it still returns promptly once ctx is done.
func TestRunWithoutPeerListenerSkipsServer(t *testing.T) {
	a := newRunTestAgent(t, nil) // ListenAddr "" and PeerToken "" by default

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runWithTimeout(t, a, ctx); err != nil {
		t.Fatalf("Run() err = %v, want nil", err)
	}
}

// TestRunErrorsWhenPeerListenerConfiguredWithoutToken proves Run refuses
// to start at all (returning promptly, without needing ctx cancellation)
// when a peer listener is configured but no token was supplied.
func TestRunErrorsWhenPeerListenerConfiguredWithoutToken(t *testing.T) {
	a := newRunTestAgent(t, func(o *Options) {
		o.Cfg.Peer.ListenAddr = "127.0.0.1:0"
		o.PeerToken = ""
	})

	err := runWithTimeout(t, a, context.Background()) // never cancelled: must not need it
	if !errors.Is(err, ErrMissingPeerToken) {
		t.Fatalf("Run() err = %v, want wrapping ErrMissingPeerToken", err)
	}
}

// TestRunPropagatesScheduleError proves a Schedule failure (an
// unparseable checkpoint time, here) surfaces from Run without starting
// anything or needing ctx cancellation.
func TestRunPropagatesScheduleError(t *testing.T) {
	a := newRunTestAgent(t, func(o *Options) {
		o.Cfg.Agent.CheckpointAt = "not-a-time"
	})

	if err := runWithTimeout(t, a, context.Background()); err == nil {
		t.Fatal("Run() err = nil, want the Schedule error")
	}
}

// TestRunPropagatesPeerServerBindError proves a peer server that fails to
// bind (address already in use) surfaces its error from Run, once ctx is
// done, rather than being silently dropped.
func TestRunPropagatesPeerServerBindError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	occupiedAddr := ln.Addr().String()

	a := newRunTestAgent(t, func(o *Options) {
		o.Cfg.Peer.ListenAddr = occupiedAddr
		o.PeerToken = "tok"
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run must still surface the (already-failed) bind error

	if err := runWithTimeout(t, a, ctx); err == nil {
		t.Fatal("Run() err = nil, want a peer server bind error")
	}
}

// TestRunExecutesAnImmediateCycleBeforeBlocking proves Run's initial
// cycle actually runs (and saves a report) even though ctx is already
// cancelled by the time Run starts blocking on it.
func TestRunExecutesAnImmediateCycleBeforeBlocking(t *testing.T) {
	fs := &fakeStore{}
	reg := registryWith(noopCheck("pi-5m", config.NodePi, initialCycleCadence))
	a := newRunTestAgent(t, func(o *Options) {
		o.Registry = reg
		o.Store = fs
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := runWithTimeout(t, a, ctx); err != nil {
		t.Fatalf("Run() err = %v, want nil", err)
	}
	if len(fs.SavedReports) != 1 {
		t.Fatalf("SavedReports = %d, want 1 (the immediate cycle ran)", len(fs.SavedReports))
	}
}
