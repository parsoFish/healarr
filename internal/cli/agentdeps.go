package cli

import (
	"context"
	"fmt"

	"github.com/parsoFish/healarr/internal/agent"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/notify"
	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
)

// AgentStoreCloser is what OpenAgentStore returns: every method
// agent.Store needs, plus Close. agent.Store itself declares no Close
// method — the Agent never owns its store's lifecycle — but `agent serve`
// and `notify test` open the store themselves and must close it on the way
// out, so their Deps hook needs a closeable handle rather than the bare
// agent.Store interface.
type AgentStoreCloser interface {
	agent.Store
	Close() error
}

var _ AgentStoreCloser = (*store.Store)(nil)

// defaultOpenAgentStore opens cfg's state store at cfg.State.DBPath — the
// same file check run/report generate open via defaultOpenStore — but
// returns it as the wider AgentStoreCloser surface agent serve/notify test
// need instead of the narrower StoreAPI.
func defaultOpenAgentStore(ctx context.Context, cfg config.Config) (AgentStoreCloser, error) {
	st, err := store.Open(ctx, cfg.State.DBPath)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// openAgentStore opens cfg's state store via deps.OpenAgentStore, falling
// back to defaultOpenAgentStore when unset (mirroring openStore in
// checkdeps.go), so a bare &Deps{} in a test reaches the same code path
// production does.
func openAgentStore(ctx context.Context, deps *Deps, cfg config.Config) (AgentStoreCloser, error) {
	openFn := deps.OpenAgentStore
	if openFn == nil {
		openFn = defaultOpenAgentStore
	}
	return openFn(ctx, cfg)
}

// defaultPeerClient builds the real HTTP peer client for cfg, or returns
// (nil, nil) when no peer is configured (cfg.Peer.PeerURL == ""): a node
// running standalone has nowhere to push/heartbeat/ping.
func defaultPeerClient(cfg config.Config, sec config.Secrets) (peer.Client, error) {
	if cfg.Peer.PeerURL == "" {
		return nil, nil
	}
	c, err := peer.New(cfg.Peer.PeerURL, sec.PeerToken)
	if err != nil {
		return nil, fmt.Errorf("peer client: %w", err)
	}
	return c, nil
}

// peerClientFor builds cfg's peer client via deps.PeerClient, falling back
// to defaultPeerClient when unset (mirroring registryFor/openStore).
func peerClientFor(deps *Deps, cfg config.Config, sec config.Secrets) (peer.Client, error) {
	fn := deps.PeerClient
	if fn == nil {
		fn = defaultPeerClient
	}
	return fn(cfg, sec)
}

// defaultSender builds the real msmtp sender for cfg, or nil when this
// node can't send mail: the NAS never calls the sender (constraints.md),
// and a Pi with no configured recipient has nothing to send to either.
func defaultSender(cfg config.Config) notify.Sender {
	if cfg.Node == config.NodeNAS || cfg.Email.To == "" {
		return nil
	}
	return notify.NewMsmtpSender(cfg.Email.MsmtpPath, cfg.Email.From)
}

// senderFor builds cfg's sender via deps.Sender, falling back to
// defaultSender when unset (mirroring peerClientFor).
func senderFor(deps *Deps, cfg config.Config) notify.Sender {
	fn := deps.Sender
	if fn == nil {
		fn = defaultSender
	}
	return fn(cfg)
}
