package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/robfig/cron/v3"

	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/web"
)

// ErrMissingPeerToken is returned by Run when cfg.Peer.ListenAddr configures
// a peer listener but Options.PeerToken was left empty. peer.NewServer
// itself refuses an empty token, but checking it here up front gives a
// clearer, agent-scoped error than that lower-level one.
var ErrMissingPeerToken = errors.New("agent: peer listener configured but no peer token set")

// Run starts the peer server (when cfg.Peer.ListenAddr is set) and the
// cron scheduler (Schedule, given runCtx so every job it runs is cancelled
// alongside it rather than running with context.Background()), runs one
// immediate check cycle before the scheduler's own entries would first
// fire, then blocks until ctx is done or the peer server fails early
// (e.g. it couldn't bind — that's treated as fatal rather than waiting for
// shutdown to notice it). Shutdown is graceful: cron.Stop waits for any
// still-running job to finish, and the peer server (peer.ListenAndServe)
// waits up to 5s for in-flight requests to finish before closing its
// listener. It returns the first non-nil error encountered, if any.
//
// runCtx is Run's own cancellable child of ctx, and everything Run starts
// gets it rather than ctx: cancelJobs is what releases a still-running job
// before cron.Stop is waited on, and it must fire however Run is leaving —
// a peer server failure with a live ctx (nothing else would ever cancel a
// job blocked on its context, so the wait below would hang) just as much
// as an ordinary ctx-triggered shutdown.
func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.Peer.ListenAddr != "" && a.peerToken == "" {
		return fmt.Errorf("agent: run: %w", ErrMissingPeerToken)
	}
	a.startedAt = a.now()

	runCtx, cancelJobs := context.WithCancel(ctx)
	defer cancelJobs()

	sched, err := a.Schedule(runCtx)
	if err != nil {
		return fmt.Errorf("agent: run: %w", err)
	}
	if err := a.logStartup(sched); err != nil {
		return fmt.Errorf("agent: run: %w", err)
	}

	peerErrCh, err := a.startPeerServer(runCtx)
	if err != nil {
		return fmt.Errorf("agent: run: %w", err)
	}
	webErrCh := a.startWebServer(runCtx)

	if _, _, err := a.RunCycle(runCtx, initialCycleCadence); err != nil {
		a.logger.Error("agent: run: initial cycle failed", "error", err)
	}

	sched.Start()
	serverErr := a.awaitShutdownOrServerFailure(ctx, peerErrCh, webErrCh)
	cancelJobs()
	<-sched.Stop().Done()

	if serverErr != nil {
		return fmt.Errorf("agent: run: server: %w", serverErr)
	}
	return nil
}

// logStartup emits the one line that says what this daemon is about to do:
// which node and version came up, in which timezone, whether the peer
// channel and the digest are wired, and how many cron entries are
// actually registered. Without it a healthy start is silent, so an
// operator reading the log after a restart cannot tell a daemon that came
// up fully configured from one whose peer or digest quietly isn't there.
// The timezone is resolved rather than echoed so the log names the zone
// the schedules really run in, including the host default an empty
// agent.timezone means.
func (a *Agent) logStartup(sched *cron.Cron) error {
	loc, err := a.cfg.Location()
	if err != nil {
		return fmt.Errorf("startup log: %w", err)
	}
	a.logger.Info("agent: starting",
		"node", a.cfg.Node,
		"version", a.version,
		"timezone", loc.String(),
		"peer_listen_addr", a.cfg.Peer.ListenAddr,
		"peer_configured", a.peer != nil,
		"heartbeat_enabled", a.peer != nil,
		"digest_enabled", a.digestEnabled(),
		"digest_at", a.cfg.Email.DigestAt,
		"checkpoint_at", a.cfg.Agent.CheckpointAt,
		"web_enabled", a.web != nil,
		"web_listen_addr", a.webAddr,
		"cron_entries", len(sched.Entries()),
	)
	return nil
}

// awaitShutdownOrServerFailure blocks until ctx is done (the normal
// shutdown trigger) or either server exits early with an error (e.g. a
// bind failure) — whichever happens first. A server error that arrives
// before ctx is done is returned immediately, without waiting for ctx or
// the other server, so a broken listener is never silently ignored until
// shutdown. When ctx fires first, it then waits for both servers' own
// (ctx-triggered) graceful shutdown to finish and joins their results. A
// A nil channel (no listener configured) never selects and waits as nil.
func (a *Agent) awaitShutdownOrServerFailure(ctx context.Context, peerErrCh, webErrCh <-chan error) error {
	select {
	case err := <-peerErrCh: // never selects while peerErrCh == nil
		return err
	case err := <-webErrCh: // never selects while webErrCh == nil
		return err
	case <-ctx.Done():
		return errors.Join(waitServerErr(peerErrCh), waitServerErr(webErrCh))
	}
}

// waitServerErr waits for ch's single buffered result, or returns nil
// immediately when ch is nil (that server was never started).
func waitServerErr(ch <-chan error) error {
	if ch == nil {
		return nil
	}
	return <-ch
}

// startPeerServer starts the peer HTTP server (this node's peer.Handler,
// bound to cfg.Peer.ListenAddr) in its own goroutine when a listen address
// is configured, returning a channel that receives its exit error once
// ctx is done and it has shut down. It returns a nil channel (and nil
// error) when no listen address is configured, so Run can skip waiting on
// it entirely.
func (a *Agent) startPeerServer(ctx context.Context) (<-chan error, error) {
	if a.cfg.Peer.ListenAddr == "" {
		return nil, nil
	}

	handler, err := peer.NewServer(a.peerToken, a.PeerHandler(), a.logger)
	if err != nil {
		return nil, fmt.Errorf("peer server: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- peer.ListenAndServe(ctx, a.cfg.Peer.ListenAddr, handler)
	}()
	return errCh, nil
}

// startWebServer starts the LAN web UI (Options.Web, bound to
// Options.WebAddr) in its own goroutine when a.web is non-nil — nil on
// the NAS, or on a Pi that couldn't build the handler before Run (see
// internal/cli/cmd_agent.go, which builds it and never hands Run a
// non-nil Web without a valid WebAddr) — returning a channel that
// receives its exit error once ctx is done and it has shut down. It
// returns a nil channel when a.web is nil, so Run can skip waiting on it
// entirely, mirroring startPeerServer. Unlike startPeerServer, there is no
// handler to build here (Options.Web already is one) and so no error
// return: the only way this can fail is the bind itself, which — exactly
// like the peer server — surfaces asynchronously on the returned channel.
func (a *Agent) startWebServer(ctx context.Context) <-chan error {
	if a.web == nil {
		return nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- web.ListenAndServe(ctx, a.webAddr, a.web)
	}()
	return errCh
}
