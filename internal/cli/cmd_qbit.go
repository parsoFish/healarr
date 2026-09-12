package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
)

// qbitGetter returns a lazy client factory: it loads config on first use
// (never at registration time), so `healarr version` never touches disk.
func qbitGetter(deps *Deps, flags *GlobalFlags) func() (qbittorrent.Client, error) {
	return func() (qbittorrent.Client, error) {
		cfg, sec, err := deps.load(flags)
		if err != nil {
			return nil, err
		}
		return deps.QBit(cfg, sec)
	}
}

func newQBitCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	get := qbitGetter(deps, flags)
	root := &cobra.Command{Use: "qbit", Short: "qBittorrent torrents and preferences"}
	root.AddCommand(
		simpleCmd("version", "qBittorrent version", get, flags,
			func(ctx context.Context, c qbittorrent.Client) (any, error) {
				v, err := c.Version(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"version": v}, nil
			}),
		simpleCmd("prefs", "Application preferences", get, flags,
			func(ctx context.Context, c qbittorrent.Client) (any, error) { return c.Preferences(ctx) }),
		qbitListCmd(get, flags),
		qbitFilesCmd(get, flags),
	)
	addQBitWriteCmds(root, get, flags)
	return root
}

func qbitListCmd(get func() (qbittorrent.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List torrents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, _ := cmd.Flags().GetString("filter")
			category, _ := cmd.Flags().GetString("category")
			c, err := get()
			if err != nil {
				return err
			}
			v, err := c.Torrents(cmd.Context(), filter, category)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
	cmd.Flags().String("filter", "", "qBittorrent filter (e.g. stalled, completed, errored); empty means all")
	cmd.Flags().String("category", "", "restrict to a category; empty means all")
	return cmd
}

func qbitFilesCmd(get func() (qbittorrent.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "files <hash>",
		Short: "List a torrent's files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := get()
			if err != nil {
				return err
			}
			v, err := c.Files(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
}
