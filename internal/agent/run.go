package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/parsoFish/healarr/internal/peer"
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

	serverErrCh, err := a.startPeerServer(runCtx)
	if err != nil {
		return fmt.Errorf("agent: run: %w", err)
	}

	if _, _, err := a.RunCycle(runCtx, initialCycleCadence); err != nil {
		a.logger.Error("agent: run: initial cycle failed", "error", err)
	}

	sched.Start()
	serverErr := a.awaitShutdownOrServerFailure(ctx, serverErrCh)
	cancelJobs()
	<-sched.Stop().Done()

	if serverErr != nil {
		return fmt.Errorf("agent: run: peer server: %w", serverErr)
	}
	return nil
}

// awaitShutdownOrServerFailure blocks until ctx is done (the normal
// shutdown trigger) or, when a peer server is running, until it exits
// early with an error (e.g. a bind failure) — whichever happens first. A
// server error that arrives before ctx is done is returned immediately,
// without waiting for ctx, so a broken listener is never silently ignored
// until shutdown. When ctx fires first, it then waits for the server's own
// (ctx-triggered) graceful shutdown to finish and returns that result
// instead. serverErrCh == nil (no listener configured) just waits on ctx.
func (a *Agent) awaitShutdownOrServerFailure(ctx context.Context, serverErrCh <-chan error) error {
	select {
	case err := <-serverErrCh: // never selects while serverErrCh == nil
		return err
	case <-ctx.Done():
		if serverErrCh == nil {
			return nil
		}
		return <-serverErrCh
	}
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
