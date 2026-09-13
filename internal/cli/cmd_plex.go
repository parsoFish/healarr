package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/plex"
)

func newPlexCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	get := newGetter(deps, flags, "plex", deps.Plex)
	root := &cobra.Command{Use: "plex", Short: "Plex server identity and libraries"}
	root.AddCommand(
		simpleCmd("identity", "Server identity", get, flags,
			func(ctx context.Context, c plex.Client) (any, error) { return c.Identity(ctx) }),
		simpleCmd("libraries", "Library sections", get, flags,
			func(ctx context.Context, c plex.Client) (any, error) { return c.Libraries(ctx) }),
		plexRecentCmd(get, flags),
		plexRefreshCmd(get, flags),
	)
	return root
}

func plexRecentCmd(get func() (plex.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recent <key>",
		Short: "Recently added items in a library",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			c, err := get()
			if err != nil {
				return err
			}
			v, err := c.RecentlyAdded(cmd.Context(), args[0], limit)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
	cmd.Flags().Int("limit", 10, "maximum items to return")
	return cmd
}

func plexRefreshCmd(get func() (plex.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh <key>",
		Short: "Trigger a library scan (W)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			desc := fmt.Sprintf("RefreshLibrary(%s)", key)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				if err := c.RefreshLibrary(cmd.Context(), key); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			})
		},
	}
}
