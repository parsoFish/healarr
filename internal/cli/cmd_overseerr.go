package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/overseerr"
)

// overseerrGetter returns a lazy client factory: it loads config on first
// use (never at registration time), so `healarr version` never touches disk.
func overseerrGetter(deps *Deps, flags *GlobalFlags) func() (overseerr.Client, error) {
	return func() (overseerr.Client, error) {
		cfg, sec, err := deps.load(flags)
		if err != nil {
			return nil, err
		}
		return deps.Overseerr(cfg, sec)
	}
}

func newOverseerrCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	get := overseerrGetter(deps, flags)
	root := &cobra.Command{Use: "overseerr", Short: "Overseerr requests and status"}
	root.AddCommand(
		simpleCmd("status", "Server status", get, flags,
			func(ctx context.Context, c overseerr.Client) (any, error) { return c.Status(ctx) }),
		overseerrRequestsCmd(get, flags),
		overseerrDeclineCmd(get, flags),
	)
	return root
}

func overseerrRequestsCmd(get func() (overseerr.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "requests",
		Short: "List media requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, _ := cmd.Flags().GetString("filter")
			c, err := get()
			if err != nil {
				return err
			}
			v, err := c.Requests(cmd.Context(), filter)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
	cmd.Flags().String("filter", "", "restrict to a request status filter; empty means all")
	return cmd
}

func overseerrDeclineCmd(get func() (overseerr.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "decline <id>",
		Short: "Decline a media request (W)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			desc := fmt.Sprintf("DeclineRequest(%d)", id)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				if err := c.DeclineRequest(cmd.Context(), id); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			})
		},
	}
}
