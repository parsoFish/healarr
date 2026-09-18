package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/agent"
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/decision"
	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
	"github.com/parsoFish/healarr/internal/version"
	"github.com/parsoFish/healarr/internal/web"
)

// errAgentServeDryRun is returned when `agent serve` is given --dry-run.
// Every other verb's --dry-run means "run in memory, never touch the
// store"; the daemon has no such observe-only mode to fall back to — it
// persists reports, pushes peer traffic and sends mail as its ordinary
// job — so honouring --dry-run would mean silently running a daemon that
// never persists anything, which is worse than refusing to start.
var errAgentServeDryRun = errors.New("agent serve does not support --dry-run; the daemon is observe-only in this phase")

// errMissingWebToken is returned when `agent serve` runs on the pi (the
// only node that ever serves the web UI) but secrets.toml has no
// web_token set. web.New itself refuses an empty token too, but checking
// it here up front names the missing secret rather than surfacing
// web.New's generic "token must not be empty" message.
var errMissingWebToken = errors.New("agent serve: pi requires secrets.web_token to serve the web ui")

func newAgentCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	root := &cobra.Command{Use: "agent", Short: "Run the healarr daemon"}
	root.AddCommand(agentServeCmd(deps, flags))
	return root
}

func agentServeCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the healarr daemon in the foreground until SIGINT/SIGTERM (--dry-run is not supported)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgentServe(cmd, deps, flags)
		},
	}
}

// runAgentServe loads config, opens the node's state store, builds an
// *agent.Agent wired to buildCheckDeps (via deps.NewAgent, agent.New by
// default) and runs it until the process receives SIGINT/SIGTERM, then
// closes the store. Any error the run itself returns is joined with a
// store-close failure rather than dropping one or the other.
func runAgentServe(cmd *cobra.Command, deps *Deps, flags *GlobalFlags) (err error) {
	if flags.DryRun {
		return errAgentServeDryRun
	}

	cfg, sec, err := deps.load(flags)
	if err != nil {
		return err
	}
	reg, err := registryFor(deps, cfg)
	if err != nil {
		return err
	}
	peerClient, err := peerClientFor(deps, cfg, sec)
	if err != nil {
		return fmt.Errorf("agent serve: peer client: %w", err)
	}

	ctx := cmd.Context()
	st, err := openAgentStore(ctx, deps, cfg)
	if err != nil {
		return fmt.Errorf("agent serve: open store: %w", err)
	}
	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("agent serve: close store: %w", closeErr))
		}
	}()

	logger := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
	webHandler, webAddr, err := buildWebHandler(deps, flags, cfg, sec, st, peerClient, logger)
	if err != nil {
		return fmt.Errorf("agent serve: %w", err)
	}

	newAgentFn := deps.NewAgent
	if newAgentFn == nil {
		newAgentFn = agent.New
	}
	a, err := newAgentFn(agent.Options{
		Cfg:      cfg,
		Registry: reg,
		Deps: func(ctx context.Context) (check.Deps, error) {
			return buildCheckDeps(ctx, deps, flags, cfg, sec)
		},
		Store:     st,
		Peer:      peerClient,
		Sender:    senderFor(deps, cfg),
		Logger:    logger,
		Now:       time.Now,
		Version:   version.Version,
		PeerToken: sec.PeerToken,
		Web:       webHandler,
		WebAddr:   webAddr,
	})
	if err != nil {
		return fmt.Errorf("agent serve: %w", err)
	}

	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if runErr := a.Run(runCtx); runErr != nil {
		err = fmt.Errorf("agent serve: %w", runErr)
	}
	return err
}

// buildWebHandler builds the LAN web UI's http.Handler and listen
// address for cfg.Node == pi (nil, "", nil on the nas — the web UI is
// pi-only, per ADR-014/constraints.md). It errors when the pi has no
// web_token configured (errMissingWebToken) or when st doesn't implement
// the wider store surfaces web.New/decision.Execute need (web.Store,
// decision.DecisionStore) — st is the same AgentStoreCloser the agent
// itself uses (openAgentStore), asserted wider here rather than opened a
// second time, mirroring cmd_cleanup.go's remediationStore assertion.
func buildWebHandler(deps *Deps, flags *GlobalFlags, cfg config.Config, sec config.Secrets, st AgentStoreCloser, peerClient peer.Client, logger *slog.Logger) (http.Handler, string, error) {
	if cfg.Node != config.NodePi {
		return nil, "", nil
	}
	if sec.WebToken == "" {
		return nil, "", errMissingWebToken
	}

	webStore, ok := st.(web.Store)
	if !ok {
		return nil, "", fmt.Errorf("web: store %T does not implement web.Store", st)
	}
	decisionStore, ok := st.(decision.DecisionStore)
	if !ok {
		return nil, "", fmt.Errorf("web: store %T does not implement decision.DecisionStore", st)
	}

	runner := &decisionRunnerAdapter{
		deps:  deps,
		flags: flags,
		cfg:   cfg,
		sec:   sec,
		store: decisionStore,
		peer:  peerClient,
	}
	handler, err := web.New(cfg, sec.WebToken, webStore, runner, logger)
	if err != nil {
		return nil, "", fmt.Errorf("web: %w", err)
	}
	return handler, cfg.Web.ListenAddr, nil
}

// decisionRunnerAdapter adapts decision.Execute to the web.DecisionRunner
// contract the web UI's decisions page runs a POST through. cfg/sec are
// the startup snapshot (set once when this adapter is constructed, never
// re-read) — only the HTTP clients are rebuilt per call, via a fresh
// check.Deps from buildCheckDeps(ctx, r.deps, r.flags, r.cfg, r.sec),
// mirroring how `decide` builds it once per CLI invocation. Those
// clients are never cached across requests, since a long-running daemon
// must not serve a decision against clients captured at startup.
type decisionRunnerAdapter struct {
	deps  *Deps
	flags *GlobalFlags
	cfg   config.Config
	sec   config.Secrets
	store decision.DecisionStore
	peer  peer.Client
}

var _ web.DecisionRunner = (*decisionRunnerAdapter)(nil)

// Execute implements web.DecisionRunner.
func (r *decisionRunnerAdapter) Execute(ctx context.Context, id int64) (store.Decision, error) {
	checkDeps, err := buildCheckDeps(ctx, r.deps, r.flags, r.cfg, r.sec)
	if err != nil {
		return store.Decision{}, fmt.Errorf("web decision runner: build check deps: %w", err)
	}
	return decision.Execute(ctx, decision.Deps{Deps: checkDeps, Store: r.store, Peer: r.peer}, id)
}
