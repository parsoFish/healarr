package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/decision"
	"github.com/parsoFish/healarr/internal/store"
)

// newDecideCmd builds the "decide" command tree: `decide keep
// <entity-key>` and `decide delete <entity-key>`, each of which creates a
// pending decision in the store and immediately executes it via
// decision.Execute (ADR-016). It is deliberately not wired into
// NewRootCmd — the Phase 4 Task 5 brief has it land standalone, tested by
// building the command directly rather than through the full cobra tree,
// so a later task can decide how (and whether) it's exposed under the
// root command alongside the still-in-flight cleanup/staleness verbs.
func newDecideCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	root := &cobra.Command{Use: "decide", Short: "Record and execute a keep/delete decision (ADR-016)"}
	root.AddCommand(
		decideKeepCmd(deps, flags),
		decideDeleteCmd(deps, flags),
	)
	return root
}

func decideKeepCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	var snoozeDays int
	cmd := &cobra.Command{
		Use:   "keep <entity-key>",
		Short: "Snooze a staleness candidate (W)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDecide(cmd, deps, flags, decision.KindKeep, args[0], snoozeDays)
		},
	}
	cmd.Flags().IntVar(&snoozeDays, "snooze", 0, "override cfg.staleness.snooze_days (days) for this decision")
	return cmd
}

func decideDeleteCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <entity-key>",
		Short: "Delete from Sonarr/Radarr, decline matching Overseerr requests, and best-effort tell the peer (W)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDecide(cmd, deps, flags, decision.KindDelete, args[0], 0)
		},
	}
}

// runDecide loads config and, unless --dry-run, opens the state store,
// creates a pending decision for entityKey/kind (overriding
// cfg.Staleness.SnoozeDays first when snoozeDays > 0), executes it via
// decision.Execute, and prints the result honouring --json. --dry-run
// never opens the store — it only prints what would happen, matching
// every other write verb's doOrDryRun contract.
func runDecide(cmd *cobra.Command, deps *Deps, flags *GlobalFlags, kind decision.Kind, entityKey string, snoozeDays int) (err error) {
	ctx := cmd.Context()
	cfg, sec, err := deps.load(flags)
	if err != nil {
		return err
	}
	if snoozeDays > 0 {
		cfg.Staleness.SnoozeDays = snoozeDays
	}

	if flags.DryRun {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "would decide %s %s\n", kind, entityKey)
		return err
	}

	st, err := store.Open(ctx, cfg.State.DBPath)
	if err != nil {
		return fmt.Errorf("decide: open store: %w", err)
	}
	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("decide: close store: %w", closeErr))
		}
	}()

	checkDeps, err := buildCheckDeps(ctx, deps, flags, cfg, sec)
	if err != nil {
		return fmt.Errorf("decide: %w", err)
	}
	peerClient, err := peerClientFor(deps, cfg, sec)
	if err != nil {
		return fmt.Errorf("decide: peer client: %w", err)
	}

	id, err := st.CreateDecision(ctx, entityKey, string(kind), time.Now())
	if err != nil {
		return fmt.Errorf("decide: create decision: %w", err)
	}

	dec, execErr := decision.Execute(ctx, decision.Deps{Deps: checkDeps, Store: st, Peer: peerClient}, id)
	if execErr != nil {
		return fmt.Errorf("decide: %w", execErr)
	}
	return Print(cmd.OutOrStdout(), flags.JSON, dec)
}
