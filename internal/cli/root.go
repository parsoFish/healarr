// Package cli wires cobra commands to service clients.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/version"
)

// GlobalFlags are the persistent flags every subcommand can read.
type GlobalFlags struct {
	ConfigPath string
	JSON       bool
	DryRun     bool
}

const defaultConfigPath = "/etc/healarr/config.toml"

// NewRootCmd builds the root command tree.
func NewRootCmd(deps *Deps) *cobra.Command {
	flags := &GlobalFlags{}
	root := &cobra.Command{
		Use:           "healarr",
		Short:         "Health agent and CLI for Plex/*arr media stacks",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flags.ConfigPath, "config", defaultConfigPath, "path to config.toml")
	root.PersistentFlags().BoolVar(&flags.JSON, "json", false, "emit JSON instead of tables")
	root.PersistentFlags().BoolVar(&flags.DryRun, "dry-run", false, "never mutate anything; print what would happen")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version and commit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "healarr %s (%s)\n", version.Version, version.Commit)
			return err
		},
	})
	root.AddCommand(
		newSonarrCmd(deps, flags),
		newRadarrCmd(deps, flags),
		newProwlarrCmd(deps, flags),
		newQBitCmd(deps, flags),
		newPlexCmd(deps, flags),
		newTautulliCmd(deps, flags),
		newOverseerrCmd(deps, flags),
		newDockerCmd(deps, flags),
		newHostCmd(deps, flags),
		newConfigCmd(deps, flags),
		newCheckCmd(deps, flags),
		newReportCmd(deps, flags),
		newStalenessCmd(deps, flags),
		newAgentCmd(deps, flags),
		newNotifyCmd(deps, flags),
		newPeerCmd(deps, flags),
	)
	return root
}
