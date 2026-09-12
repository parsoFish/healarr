package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
)

// addQBitWriteCmds registers every mutating qBittorrent verb on root. Split
// out of cmd_qbit.go to keep each file under the 150-line target.
func addQBitWriteCmds(root *cobra.Command, get func() (qbittorrent.Client, error), flags *GlobalFlags) {
	root.AddCommand(
		qbitDeleteCmd(get, flags),
		qbitReannounceCmd(get, flags),
		qbitResumeCmd(get, flags),
	)
}

func qbitDeleteCmd(get func() (qbittorrent.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <hash...>",
		Short: "Delete torrents (W)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, hashes []string) error {
			deleteFiles, _ := cmd.Flags().GetBool("files")
			desc := fmt.Sprintf("Delete(%v,%t)", hashes, deleteFiles)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				if err := c.Delete(cmd.Context(), hashes, deleteFiles); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			})
		},
	}
	cmd.Flags().Bool("files", false, "also delete the downloaded files")
	return cmd
}

func qbitReannounceCmd(get func() (qbittorrent.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "reannounce <hash...>",
		Short: "Force-reannounce torrents to their trackers (W)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, hashes []string) error {
			desc := fmt.Sprintf("Reannounce(%v)", hashes)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				if err := c.Reannounce(cmd.Context(), hashes); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			})
		},
	}
}

func qbitResumeCmd(get func() (qbittorrent.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "resume <hash...>",
		Short: "Resume paused torrents (W)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, hashes []string) error {
			desc := fmt.Sprintf("Resume(%v)", hashes)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				if err := c.Resume(cmd.Context(), hashes); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			})
		},
	}
}
