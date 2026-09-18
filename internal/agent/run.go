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
// cron scheduler (Schedule), runs one immediate check cycle before the
// scheduler's own entries would first fire, then blocks until ctx is
// done. Shutdown is graceful: cron.Stop waits for any still-running job to
// finish, and the peer server (peer.ListenAndServe) waits up to 5s for
// in-flight requests to finish before closing its listener. It returns the
// first non-nil error encountered, if any.
func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.Peer.ListenAddr != "" && a.peerToken == "" {
		return fmt.Errorf("agent: run: %w", ErrMissingPeerToken)
	}

	sched, err := a.Schedule()
	if err != nil {
		return fmt.Errorf("agent: run: %w", err)
	}

	serverErrCh, err := a.startPeerServer(ctx)
	if err != nil {
		return fmt.Errorf("agent: run: %w", err)
	}

	if _, _, err := a.RunCycle(ctx, initialCycleCadence); err != nil {
		a.logger.Error("agent: run: initial cycle failed", "error", err)
	}

	sched.Start()
	<-ctx.Done()
	<-sched.Stop().Done()

	if serverErrCh == nil {
		return nil
	}
	if err := <-serverErrCh; err != nil {
		return fmt.Errorf("agent: run: peer server: %w", err)
	}
	return nil
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
