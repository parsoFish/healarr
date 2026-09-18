package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/agent"
	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/version"
)

// errAgentServeDryRun is returned when `agent serve` is given --dry-run.
// Every other verb's --dry-run means "run in memory, never touch the
// store"; the daemon has no such observe-only mode to fall back to — it
// persists reports, pushes peer traffic and sends mail as its ordinary
// job — so honouring --dry-run would mean silently running a daemon that
// never persists anything, which is worse than refusing to start.
var errAgentServeDryRun = errors.New("agent serve does not support --dry-run; the daemon is observe-only in this phase")

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
		Logger:    slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil)),
		Now:       time.Now,
		Version:   version.Version,
		PeerToken: sec.PeerToken,
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
