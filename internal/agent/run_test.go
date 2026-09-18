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
	"github.com/parsoFish/healarr/internal/notify"
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
			HeartbeatInterval:    5 * time.Minute,
			CheckpointAt:         "03:00",
			PeerStaleAfter:       15 * time.Minute,
			PeerMessageRetention: 30 * 24 * time.Hour,
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

// TestRunPropagatesPeerServerBindErrorPromptly proves a peer server that
// fails to bind (address already in use) surfaces its error from Run on
// its own — a bind failure is fatal, not something Run should have to
// wait for shutdown to notice — with ctx never cancelled at all.
func TestRunPropagatesPeerServerBindErrorPromptly(t *testing.T) {
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

	// ctx is a live, never-cancelled context: Run must still return the
	// bind error on its own rather than hanging until something cancels it.
	if err := runWithTimeout(t, a, context.Background()); err == nil {
		t.Fatal("Run() err = nil, want a peer server bind error")
	}
}

// TestRunCancelsInFlightJobsOnShutdown proves a scheduled job runs with
// Run's own ctx, not context.Background(): a heartbeat job that blocks
// until its ctx is done would hang cron.Stop() (and so Run) forever under
// the old behaviour, but returns promptly once ctx is cancelled here.
func TestRunCancelsInFlightJobsOnShutdown(t *testing.T) {
	tp := newTestPeerClient(true) // Heartbeat blocks until ctx.Done()
	a := newRunTestAgent(t, func(o *Options) {
		o.Peer = tp
		o.Cfg.Agent.HeartbeatInterval = time.Millisecond // fires almost immediately
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	select {
	case <-tp.Called():
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat job never started")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() err = %v, want nil", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run() did not return within 500ms of ctx cancellation (job's ctx wasn't cancelled)")
	}
}

// TestRunSetsStartedAtFromNow proves Run stamps startedAt from a.now()
// before dispatching any job (fixedNow makes this exact, not just
// "close enough"), so SendHeartbeat can report a real uptime.
func TestRunSetsStartedAtFromNow(t *testing.T) {
	a := newRunTestAgent(t, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWithTimeout(t, a, ctx); err != nil {
		t.Fatalf("Run() err = %v, want nil", err)
	}

	if !a.startedAt.Equal(runT0) {
		t.Fatalf("startedAt = %v, want %v (a.now() at Run's start)", a.startedAt, runT0)
	}
}

// TestSendHeartbeatReportsUptimeSinceStartedAt proves the heartbeat's
// UptimeSeconds is now - startedAt when startedAt has been set (as Run
// does).
func TestSendHeartbeatReportsUptimeSinceStartedAt(t *testing.T) {
	tp := newTestPeerClient(false)
	a := newRunTestAgent(t, func(o *Options) { o.Peer = tp })
	a.startedAt = runT0.Add(-90 * time.Second)

	if err := a.SendHeartbeat(context.Background()); err != nil {
		t.Fatalf("SendHeartbeat() err = %v", err)
	}
	hbs := tp.Heartbeats()
	if len(hbs) != 1 {
		t.Fatalf("Heartbeats() = %d, want 1", len(hbs))
	}
	if hbs[0].UptimeSeconds != 90 {
		t.Fatalf("UptimeSeconds = %d, want 90", hbs[0].UptimeSeconds)
	}
}

// TestSendHeartbeatUptimeZeroWhenStartedAtUnset proves a direct
// SendHeartbeat call that bypasses Run (startedAt left at its zero value)
// reports 0 uptime rather than a bogus multi-century duration.
func TestSendHeartbeatUptimeZeroWhenStartedAtUnset(t *testing.T) {
	tp := newTestPeerClient(false)
	a := newRunTestAgent(t, func(o *Options) { o.Peer = tp })

	if err := a.SendHeartbeat(context.Background()); err != nil {
		t.Fatalf("SendHeartbeat() err = %v", err)
	}
	hbs := tp.Heartbeats()
	if len(hbs) != 1 {
		t.Fatalf("Heartbeats() = %d, want 1", len(hbs))
	}
	if hbs[0].UptimeSeconds != 0 {
		t.Fatalf("UptimeSeconds = %d, want 0 (startedAt never set)", hbs[0].UptimeSeconds)
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

// TestRunCancelsJobsWhenPeerServerFails proves the server-failure path
// cancels the scheduler's jobs before waiting for them. ctx is live and
// never cancelled, so the only thing that can release a job blocked on its
// own context (here a heartbeat that waits for ctx.Done()) is Run itself:
// without that cancellation, cron.Stop()'s wait for the in-flight job never
// finishes and Run hangs instead of surfacing the bind error. The context
// the initial cycle was handed (captured through Options.Deps, which
// RunCycle calls with exactly that context) must therefore be done by the
// time Run returns.
func TestRunCancelsJobsWhenPeerServerFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	jobCtx := make(chan context.Context, 1)
	tp := newTestPeerClient(true) // Heartbeat blocks until its ctx is done
	a := newRunTestAgent(t, func(o *Options) {
		o.Cfg.Peer.ListenAddr = ln.Addr().String() // already in use: the bind fails
		o.PeerToken = "tok"
		o.Peer = tp
		o.Cfg.Agent.HeartbeatInterval = time.Millisecond // fires almost immediately
		o.Registry = registryWith(noopCheck("pi-5m", config.NodePi, initialCycleCadence))
		o.Deps = func(ctx context.Context) (check.Deps, error) {
			select {
			case jobCtx <- ctx:
			default:
			}
			return check.Deps{Node: config.NodePi, Now: func() time.Time { return runT0 }}, nil
		}
	})

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	select {
	case runErr := <-done:
		if runErr == nil {
			t.Fatal("Run() err = nil, want the peer server bind error")
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return within 1s of the peer server's bind failure")
	}

	select {
	case got := <-jobCtx:
		if got.Err() == nil {
			t.Fatal("the context Run gave its jobs was never cancelled")
		}
	default:
		t.Fatal("the initial cycle never ran, so no job context was captured")
	}
}

// TestRunLogsStartupSummary proves the daemon says what it is going to do
// as it starts: an operator reading `journalctl -u healarr` after a
// restart can tell from one line which node and version came up, which
// schedules are live, and whether the peer and digest are wired — without
// waiting for the first cycle to produce evidence.
func TestRunLogsStartupSummary(t *testing.T) {
	var logBuf strings.Builder
	a := newRunTestAgent(t, func(o *Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
		o.Peer = newTestPeerClient(false)
		o.Sender = &notify.FakeSender{}
		o.Cfg.Agent.Timezone = "Australia/Brisbane"
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWithTimeout(t, a, ctx); err != nil {
		t.Fatalf("Run() err = %v, want nil", err)
	}

	out := logBuf.String()
	for _, want := range []string{
		"node=pi", "version=v-test", "timezone=Australia/Brisbane",
		"peer_configured=true", "heartbeat_enabled=true",
		"digest_enabled=true", "digest_at=07:00", "checkpoint_at=03:00",
		"cron_entries=",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("startup log = %q, want it to report %q", out, want)
		}
	}
}
