package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/tautulli"
)

// tautulliGetter returns a lazy client factory: it loads config on first
// use (never at registration time), so `healarr version` never touches disk.
func tautulliGetter(deps *Deps, flags *GlobalFlags) func() (tautulli.Client, error) {
	return func() (tautulli.Client, error) {
		cfg, sec, err := deps.load(flags)
		if err != nil {
			return nil, err
		}
		return deps.Tautulli(cfg, sec)
	}
}

func newTautulliCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	get := tautulliGetter(deps, flags)
	root := &cobra.Command{Use: "tautulli", Short: "Tautulli watch history and activity"}
	root.AddCommand(
		simpleCmd("ping", "Check connectivity", get, flags,
			func(ctx context.Context, c tautulli.Client) (any, error) {
				if err := c.Ping(ctx); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			}),
		simpleCmd("activity", "Current stream count", get, flags,
			func(ctx context.Context, c tautulli.Client) (any, error) {
				n, err := c.ActivityCount(ctx)
				if err != nil {
					return nil, err
				}
				return map[string]any{"activity": n}, nil
			}),
		tautulliHistoryCmd(get, flags),
	)
	return root
}

func tautulliHistoryCmd(get func() (tautulli.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Watch history",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sinceStr, _ := cmd.Flags().GetString("since")
			length, _ := cmd.Flags().GetInt("length")
			since, err := parseSince(sinceStr)
			if err != nil {
				return err
			}
			c, err := get()
			if err != nil {
				return err
			}
			v, err := c.History(cmd.Context(), since, length)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
	cmd.Flags().String("since", "30d", "only include history after this (Go duration or Nd days)")
	cmd.Flags().Int("length", 25, "maximum rows to return")
	return cmd
}
