package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/radarr"
)

// radarrGetter returns a lazy client factory: it loads config on first use
// (never at registration time), so `healarr version` never touches disk.
func radarrGetter(deps *Deps, flags *GlobalFlags) func() (radarr.Client, error) {
	return func() (radarr.Client, error) {
		cfg, sec, err := deps.load(flags)
		if err != nil {
			return nil, err
		}
		return deps.Radarr(cfg, sec)
	}
}

func newRadarrCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	get := radarrGetter(deps, flags)
	root := &cobra.Command{Use: "radarr", Short: "Radarr queue, movies and health"}
	root.AddCommand(
		simpleCmd("health", "Health check items", get, flags,
			func(ctx context.Context, c radarr.Client) (any, error) { return c.Health(ctx) }),
		simpleCmd("queue", "Download queue", get, flags,
			func(ctx context.Context, c radarr.Client) (any, error) { return c.Queue(ctx) }),
		simpleCmd("movies", "All movies", get, flags,
			func(ctx context.Context, c radarr.Client) (any, error) { return c.Movies(ctx) }),
		simpleCmd("rootfolders", "Root folders", get, flags,
			func(ctx context.Context, c radarr.Client) (any, error) { return c.RootFolders(ctx) }),
		radarrMissingCmd(get, flags),
		radarrHistoryCmd(get, flags),
	)
	addRadarrWriteCmds(root, get, flags)
	return root
}

func radarrMissingCmd(get func() (radarr.Client, error), flags *GlobalFlags) *cobra.Command {
	return simpleCmd("missing", "Count of wanted/missing movies", get, flags,
		func(ctx context.Context, c radarr.Client) (any, error) {
			n, err := c.WantedMissingCount(ctx)
			if err != nil {
				return nil, err
			}
			return map[string]any{"missing": n}, nil
		})
}

func radarrHistoryCmd(get func() (radarr.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Recent history",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sinceStr, _ := cmd.Flags().GetString("since")
			eventType, _ := cmd.Flags().GetString("type")
			since, err := parseSince(sinceStr)
			if err != nil {
				return err
			}
			c, err := get()
			if err != nil {
				return err
			}
			v, err := c.History(cmd.Context(), since, eventType)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
	cmd.Flags().String("since", "24h", "only include history after this (Go duration or Nd days)")
	cmd.Flags().String("type", "", "filter by event type")
	return cmd
}
