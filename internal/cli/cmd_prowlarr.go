package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/prowlarr"
)

func newProwlarrCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	get := newGetter(deps, flags, "prowlarr", deps.Prowlarr)
	root := &cobra.Command{Use: "prowlarr", Short: "Prowlarr indexers and health"}
	root.AddCommand(
		simpleCmd("health", "Health check items", get, flags,
			func(ctx context.Context, c prowlarr.Client) (any, error) { return c.Health(ctx) }),
		simpleCmd("indexers", "Configured indexers", get, flags,
			func(ctx context.Context, c prowlarr.Client) (any, error) { return c.Indexers(ctx) }),
		simpleCmd("status", "Per-indexer failure/backoff status", get, flags,
			func(ctx context.Context, c prowlarr.Client) (any, error) { return c.IndexerStatus(ctx) }),
		prowlarrIndexerDeleteCmd(get, flags),
		prowlarrIndexerTestCmd(get, flags),
	)
	return root
}

func prowlarrIndexerDeleteCmd(get func() (prowlarr.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "indexer-delete <id>",
		Short: "Delete an indexer (W)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			desc := fmt.Sprintf("DeleteIndexer(%d)", id)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				if err := c.DeleteIndexer(cmd.Context(), id); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, nil
			})
		},
	}
}

// prowlarrIndexerTestCmd tests an indexer's connectivity. It is not marked
// (W) in the verb list (it mutates nothing in Prowlarr's own config), so it
// always runs, ignoring --dry-run.
func prowlarrIndexerTestCmd(get func() (prowlarr.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "indexer-test <id>",
		Short: "Test an indexer's connectivity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			c, err := get()
			if err != nil {
				return err
			}
			if err := c.TestIndexer(cmd.Context(), id); err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, map[string]any{"ok": true})
		},
	}
}
