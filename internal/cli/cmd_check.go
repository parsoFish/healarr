package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// newCheckCmd builds the "check" command tree: list lists the catalogue,
// run executes a subset of it (see cmd_check_run.go).
func newCheckCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	root := &cobra.Command{Use: "check", Short: "Inspect and run the health check catalogue"}
	root.AddCommand(checkListCmd(deps, flags))
	root.AddCommand(checkRunCmd(deps, flags))
	return root
}

// checkListRow is one row of `check list` output: the catalogue's ID,
// which node(s) it applies to, its suggested tier, and its schedule.
type checkListRow struct {
	ID      string
	Nodes   string
	Tier    string
	Cadence string
}

func checkListCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every registered check (all nodes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _, err := deps.load(flags)
			if err != nil {
				return err
			}
			reg, err := registryFor(deps, cfg)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, checkListRows(reg.All()))
		},
	}
}

// checkListRows projects the catalogue's checks into printable rows, in
// registration order.
func checkListRows(all []check.Check) []checkListRow {
	rows := make([]checkListRow, 0, len(all))
	for _, c := range all {
		rows = append(rows, checkListRow{
			ID:      c.ID,
			Nodes:   nodesString(c.Nodes),
			Tier:    string(c.Tier),
			Cadence: c.Cadence.String(),
		})
	}
	return rows
}

// nodesString renders a Check.Nodes slice as a comma-separated list, e.g.
// "pi,nas".
func nodesString(nodes []config.Node) string {
	strs := make([]string, len(nodes))
	for i, n := range nodes {
		strs[i] = string(n)
	}
	return strings.Join(strs, ",")
}
