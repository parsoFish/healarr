package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// errCheckRunFlags is returned when check run is given neither or both of
// --all/--id.
var errCheckRunFlags = errors.New("check run: exactly one of --all or --id is required")

func checkRunCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	var (
		all bool
		id  string
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run checks against this node (--dry-run: never touch the store)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCheckRun(cmd, deps, flags, all, id)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "run every check that applies to this node")
	cmd.Flags().StringVar(&id, "id", "", "run a single check by id")
	return cmd
}

// runCheckRun implements `check run`: exactly one of --all/--id selects
// the checks, the engine runs them against this node's clients, and
// (unless --dry-run) the result is persisted. A failure loading config,
// building the registry/check deps, or talking to the store is returned as
// an error; findings and per-check errors are data, printed but never
// turned into a non-zero exit.
func runCheckRun(cmd *cobra.Command, deps *Deps, flags *GlobalFlags, all bool, id string) (err error) {
	if all == (id != "") {
		return errCheckRunFlags
	}
	ctx := cmd.Context()

	cfg, sec, err := deps.load(flags)
	if err != nil {
		return err
	}
	reg, err := registryFor(deps, cfg)
	if err != nil {
		return err
	}
	selected, err := selectChecks(reg, cfg.Node, all, id)
	if err != nil {
		return err
	}
	checkDeps, err := buildCheckDeps(ctx, deps, flags, cfg, sec)
	if err != nil {
		return err
	}

	var st StoreAPI
	if !flags.DryRun {
		st, err = deps.OpenStore(ctx, cfg)
		if err != nil {
			return fmt.Errorf("check run: open store: %w", err)
		}
		defer func() {
			if closeErr := st.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("check run: close store: %w", closeErr))
			}
		}()

		var (
			prev  check.Report
			found bool
		)
		prev, found, err = st.LatestReport(ctx, cfg.Node)
		if err != nil {
			return fmt.Errorf("check run: previous report: %w", err)
		}
		if found {
			checkDeps.Previous = &prev
		}
	}

	rep := check.Run(ctx, selected, checkDeps, cfg.Checks.Timeout)

	var (
		persisted bool
		upsert    store.UpsertSummary
	)
	if !flags.DryRun {
		_, upsert, err = st.SaveReport(ctx, rep)
		if err != nil {
			return fmt.Errorf("check run: save report: %w", err)
		}
		persisted = true
	}

	return printCheckRun(cmd.OutOrStdout(), flags.JSON, rep, persisted, upsert)
}

// selectChecks resolves --all/--id into the checks to run: --all is every
// check that applies to node, --id is that one check provided it exists
// and applies to node.
func selectChecks(reg *check.Registry, node config.Node, all bool, id string) ([]check.Check, error) {
	if all {
		return reg.ForNode(node), nil
	}
	c, ok := reg.ByID(id)
	if !ok {
		return nil, fmt.Errorf("check run: check %s not found", id)
	}
	if !c.AppliesTo(node) {
		return nil, fmt.Errorf("check %s does not run on node %s", id, node)
	}
	return []check.Check{c}, nil
}

// checkRunOutput is the JSON shape for `check run`.
type checkRunOutput struct {
	Report    check.Report `json:"report"`
	Persisted bool         `json:"persisted"`
	Upsert    upsertJSON   `json:"upsert"`
}

// upsertJSON gives store.UpsertSummary's fields lowercase JSON keys without
// changing the store package.
type upsertJSON struct {
	New      int `json:"new"`
	Updated  int `json:"updated"`
	Resolved int `json:"resolved"`
}

// printCheckRun writes the run's outcome: JSON emits the full report plus
// persistence metadata; the table lists one row per finding followed by a
// checks/findings summary line and, if any check failed, its errors.
func printCheckRun(w io.Writer, jsonMode bool, rep check.Report, persisted bool, upsert store.UpsertSummary) error {
	if jsonMode {
		out := checkRunOutput{
			Report:    rep,
			Persisted: persisted,
			Upsert:    upsertJSON{New: upsert.New, Updated: upsert.Updated, Resolved: upsert.Resolved},
		}
		raw, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return fmt.Errorf("cli: marshal check run json: %w", err)
		}
		_, err = fmt.Fprintln(w, string(raw))
		return err
	}
	return printCheckRunTable(w, rep)
}

// checkFindingRow is one finding row of `check run`'s table output:
// Severity, CheckID, EntityKey, Summary.
type checkFindingRow struct {
	Severity  string
	CheckID   string
	EntityKey string
	Summary   string
}

func printCheckRunTable(w io.Writer, rep check.Report) error {
	rows := make([]checkFindingRow, 0, len(rep.Findings))
	for _, f := range rep.Findings {
		rows = append(rows, checkFindingRow{
			Severity:  string(f.Severity),
			CheckID:   f.CheckID,
			EntityKey: f.EntityKey,
			Summary:   f.Summary,
		})
	}
	if err := Print(w, false, rows); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "checks: %d run, %d failed, %d skipped; findings: %d\n",
		len(rep.Ran), len(rep.Errors), len(rep.Skipped), len(rep.Findings)); err != nil {
		return err
	}
	if len(rep.Errors) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "errors:"); err != nil {
		return err
	}
	for _, e := range rep.Errors {
		if _, err := fmt.Fprintf(w, "  %s: %s\n", e.CheckID, e.Error); err != nil {
			return err
		}
	}
	return nil
}
