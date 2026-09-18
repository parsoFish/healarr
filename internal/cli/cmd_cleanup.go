package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/cleanup"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// remediationStore is the subset of the state store `cleanup <kind>`
// needs beyond StoreAPI (checkdeps.go): recording one remediations row.
// It is its own interface, rather than an addition to StoreAPI, so this
// file's dependency surface stays self-contained; the real *store.Store
// and this package's test fakes both implement it.
type remediationStore interface {
	RecordRemediation(ctx context.Context, r store.Remediation) (int64, error)
	Close() error
}

// newCleanupCmd builds `healarr cleanup <kind>`. It is exported but not
// wired into NewRootCmd here — root.go registration is another task's
// job; a caller (production or test) attaches it with
// root.AddCommand(newCleanupCmd(deps, flags)).
func newCleanupCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	kinds := []string{string(cleanup.KindRecycle), string(cleanup.KindOrphans), string(cleanup.KindDocker), string(cleanup.KindSeeded)}
	cmd := &cobra.Command{
		Use:       "cleanup <kind>",
		Short:     "Plan (and, when allowed, execute) a cleanup: recycle, orphans, docker, or seeded",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: kinds,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCleanup(cmd, deps, flags, cleanup.Kind(args[0]))
		},
	}
	return cmd
}

// plannerForKind finds the registered Planner for kind.
func plannerForKind(kind cleanup.Kind) (cleanup.Planner, error) {
	for _, p := range cleanup.All() {
		if p.Kind() == kind {
			return p, nil
		}
	}
	return nil, fmt.Errorf("cleanup: unknown kind %q", kind)
}

// nodeAllowed reports whether node is one of nodes.
func nodeAllowed(nodes []config.Node, node config.Node) bool {
	for _, n := range nodes {
		if n == node {
			return true
		}
	}
	return false
}

// runCleanup implements `cleanup <kind>`: it always plans and prints the
// plan; with the global --dry-run flag it stops there and never opens the
// store (mirroring `check run`). Otherwise it always records exactly one
// remediations row, and additionally executes the plan for real only when
// config.Actions.Enabled is true and config.Cleanup.DryRun is false —
// otherwise it records the attempt as "blocked" (actions disabled) or
// "planned" (actions enabled but the cleanup subsystem's own dry_run
// safety flag is still on) without ever calling cleanup.Execute.
func runCleanup(cmd *cobra.Command, deps *Deps, flags *GlobalFlags, kind cleanup.Kind) error {
	ctx := cmd.Context()

	planner, err := plannerForKind(kind)
	if err != nil {
		return err
	}
	cfg, sec, err := deps.load(flags)
	if err != nil {
		return err
	}
	if !nodeAllowed(planner.Nodes(), cfg.Node) {
		return fmt.Errorf("cleanup: %s does not run on node %s", kind, cfg.Node)
	}
	checkDeps, err := buildCheckDeps(ctx, deps, flags, cfg, sec)
	if err != nil {
		return err
	}

	plan, planErr := planner.Plan(ctx, checkDeps)
	if planErr != nil {
		if !flags.DryRun {
			if recErr := recordCleanup(ctx, deps, cfg, checkDeps.Now(), kind, true, "failed", planErr.Error()); recErr != nil {
				return errors.Join(planErr, recErr)
			}
		}
		return planErr
	}

	return finishCleanup(cmd, deps, flags, checkDeps, cfg, kind, plan)
}

// finishCleanup handles everything after a successful Plan: the
// dry-run/blocked/planned/executed branching, remediation recording, and
// output.
func finishCleanup(cmd *cobra.Command, deps *Deps, flags *GlobalFlags, checkDeps check.Deps, cfg config.Config, kind cleanup.Kind, plan cleanup.Plan) error {
	out := cleanupOutput{Kind: kind, Node: cfg.Node, Plan: plan}

	if flags.DryRun {
		out.Status, out.DryRun = "planned", true
		return printCleanup(cmd.OutOrStdout(), flags.JSON, out)
	}

	ctx := cmd.Context()
	configAllows := cfg.Actions.Enabled && !cfg.Cleanup.DryRun
	if !configAllows {
		status := "planned"
		if !cfg.Actions.Enabled {
			status = "blocked"
		}
		out.Status, out.DryRun = status, true
		if err := recordCleanup(ctx, deps, cfg, checkDeps.Now(), kind, true, status, planSummary(plan)); err != nil {
			return err
		}
		return printCleanup(cmd.OutOrStdout(), flags.JSON, out)
	}

	result, execErr := cleanup.Execute(ctx, checkDeps, plan)
	if execErr != nil {
		if recErr := recordCleanup(ctx, deps, cfg, checkDeps.Now(), kind, false, "failed", execErr.Error()); recErr != nil {
			return errors.Join(execErr, recErr)
		}
		return execErr
	}
	out.Result, out.Status, out.DryRun = &result, "executed", false
	if err := recordCleanup(ctx, deps, cfg, checkDeps.Now(), kind, false, "executed", resultSummary(result)); err != nil {
		return err
	}
	return printCleanup(cmd.OutOrStdout(), flags.JSON, out)
}

// recordCleanup opens the store, writes one remediations row for this
// cleanup attempt, and closes the store; it is never called under
// --dry-run (runCleanup/finishCleanup enforce that). now comes from the
// same check.Deps.Now clock the plan/execute call used, so a test with a
// fixed clock sees it in the recorded row too.
func recordCleanup(ctx context.Context, deps *Deps, cfg config.Config, now time.Time, kind cleanup.Kind, dryRun bool, status, detail string) (err error) {
	raw, err := openStore(ctx, deps, cfg)
	if err != nil {
		return fmt.Errorf("cleanup: open store: %w", err)
	}
	defer func() {
		if closeErr := raw.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("cleanup: close store: %w", closeErr))
		}
	}()

	rs, ok := raw.(remediationStore)
	if !ok {
		return fmt.Errorf("cleanup: store %T does not support recording remediations", raw)
	}
	r := store.Remediation{
		Node:      cfg.Node,
		Action:    "cleanup:" + string(kind),
		Tier:      "correct",
		Status:    status,
		Detail:    detail,
		DryRun:    dryRun,
		CreatedAt: now,
	}
	if _, err = rs.RecordRemediation(ctx, r); err != nil {
		return fmt.Errorf("cleanup: record remediation: %w", err)
	}
	return nil
}

// planSummary renders a Plan's counts/bytes for the remediations row's
// Detail column.
func planSummary(p cleanup.Plan) string {
	return fmt.Sprintf("%d item(s) planned, %d byte(s)", len(p.Items), p.Bytes)
}

// resultSummary renders a Result's counts/bytes/errors for the
// remediations row's Detail column.
func resultSummary(r cleanup.Result) string {
	s := fmt.Sprintf("%d item(s) executed, %d byte(s) reclaimed", r.Executed, r.Bytes)
	if len(r.Errors) > 0 {
		s += fmt.Sprintf(", %d error(s): %s", len(r.Errors), strings.Join(r.Errors, "; "))
	}
	return s
}

// cleanupOutput is the JSON/table shape `cleanup <kind>` prints.
type cleanupOutput struct {
	Kind   cleanup.Kind    `json:"kind"`
	Node   config.Node     `json:"node"`
	Plan   cleanup.Plan    `json:"plan"`
	Status string          `json:"status"`
	DryRun bool            `json:"dryRun"`
	Result *cleanup.Result `json:"result,omitempty"`
}

func printCleanup(w io.Writer, jsonMode bool, out cleanupOutput) error {
	if jsonMode {
		raw, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("cli: marshal cleanup json: %w", err)
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	return printCleanupTable(w, out)
}

// cleanupItemRow is one PlanItem row of the table output.
type cleanupItemRow struct {
	Key    string
	Detail string
	Bytes  int64
}

func printCleanupTable(w io.Writer, out cleanupOutput) error {
	rows := make([]cleanupItemRow, 0, len(out.Plan.Items))
	for _, it := range out.Plan.Items {
		rows = append(rows, cleanupItemRow{Key: it.Key, Detail: it.Detail, Bytes: it.Bytes})
	}
	if err := Print(w, false, rows); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "plan: %d item(s), %d byte(s); status: %s (dryRun=%t)\n",
		len(out.Plan.Items), out.Plan.Bytes, out.Status, out.DryRun); err != nil {
		return err
	}
	if out.Result == nil {
		return nil
	}
	_, err := fmt.Fprintf(w, "result: %d executed, %d byte(s) reclaimed, %d error(s)\n",
		out.Result.Executed, out.Result.Bytes, len(out.Result.Errors))
	return err
}
