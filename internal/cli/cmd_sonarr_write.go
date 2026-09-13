package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/sonarr"
)

// addSonarrWriteCmds registers every mutating Sonarr verb on root. Split out
// of cmd_sonarr.go to keep each file under the 150-line target.
func addSonarrWriteCmds(root *cobra.Command, get func() (sonarr.Client, error), flags *GlobalFlags) {
	root.AddCommand(
		sonarrQueueDeleteCmd(get, flags),
		sonarrMonitorCmd(get, flags),
		sonarrSeriesDeleteCmd(get, flags),
		sonarrCommandCmd(get, flags),
	)
}

func sonarrQueueDeleteCmd(get func() (sonarr.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue-delete <id>",
		Short: "Remove a queue item (W)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			removeFromClient, _ := cmd.Flags().GetBool("remove-from-client")
			blocklist, _ := cmd.Flags().GetBool("blocklist")
			desc := fmt.Sprintf("DeleteQueueItem(%d,%t,%t)", id, removeFromClient, blocklist)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				if err := c.DeleteQueueItem(cmd.Context(), id, removeFromClient, blocklist); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			})
		},
	}
	cmd.Flags().Bool("remove-from-client", false, "also remove from the download client")
	cmd.Flags().Bool("blocklist", false, "blocklist the release")
	return cmd
}

func sonarrMonitorCmd(get func() (sonarr.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "monitor <id> <on|off>",
		Short: "Set a series' monitored state (W)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			monitored, err := parseOnOff(args[1])
			if err != nil {
				return err
			}
			desc := fmt.Sprintf("UpdateSeriesMonitored(%d,%t)", id, monitored)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				if err := c.UpdateSeriesMonitored(cmd.Context(), id, monitored); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			})
		},
	}
}

func sonarrSeriesDeleteCmd(get func() (sonarr.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "series-delete <id>",
		Short: "Delete a series (W)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			deleteFiles, _ := cmd.Flags().GetBool("delete-files")
			desc := fmt.Sprintf("DeleteSeries(%d,%t,false)", id, deleteFiles)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				if err := c.DeleteSeries(cmd.Context(), id, deleteFiles, false); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			})
		},
	}
	cmd.Flags().Bool("delete-files", false, "also delete files from disk")
	return cmd
}

func sonarrCommandCmd(get func() (sonarr.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "command <name>",
		Short: "Run a Sonarr command (W)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			desc := fmt.Sprintf("RunCommand(%s)", name)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				id, err := c.RunCommand(cmd.Context(), name, nil)
				if err != nil {
					return nil, err
				}
				return map[string]any{"id": id}, nil
			})
		},
	}
}
