package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/docker"
)

// dockerGetter returns a lazy client factory: it loads config on first use
// (never at registration time), so `healarr version` never touches disk.
func dockerGetter(deps *Deps, flags *GlobalFlags) func() (docker.Client, error) {
	return func() (docker.Client, error) {
		cfg, sec, err := deps.load(flags)
		if err != nil {
			return nil, err
		}
		return deps.Docker(cfg, sec)
	}
}

func newDockerCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	get := dockerGetter(deps, flags)
	root := &cobra.Command{Use: "docker", Short: "Docker containers, logs and disk usage"}
	root.AddCommand(
		simpleCmd("ps", "List containers", get, flags,
			func(ctx context.Context, c docker.Client) (any, error) { return c.Containers(ctx) }),
		simpleCmd("df", "Image and build-cache disk usage", get, flags,
			func(ctx context.Context, c docker.Client) (any, error) { return c.DiskUsage(ctx) }),
		dockerLogsCmd(get, flags),
		dockerPruneCmd(get, flags),
		dockerExecCmd(get, flags),
	)
	return root
}

func dockerLogsCmd(get func() (docker.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Tail a container's logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tail, _ := cmd.Flags().GetInt("tail")
			c, err := get()
			if err != nil {
				return err
			}
			v, err := c.Logs(cmd.Context(), args[0], tail)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
	cmd.Flags().Int("tail", 100, "number of trailing lines to fetch")
	return cmd
}

// dockerPruneCmd defaults --dangling to true, matching `docker image prune`'s
// own safe default; pass --dangling=false to remove every unused image.
func dockerPruneCmd(get func() (docker.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove unused images (W)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dangling, _ := cmd.Flags().GetBool("dangling")
			desc := fmt.Sprintf("PruneImages(%t)", dangling)
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				reclaimed, err := c.PruneImages(cmd.Context(), dangling)
				if err != nil {
					return nil, err
				}
				return map[string]any{"reclaimed_bytes": reclaimed}, nil
			})
		},
	}
	cmd.Flags().Bool("dangling", true, "prune dangling images only; false prunes every unused image")
	return cmd
}

// dockerExecCmd is not marked (W): it shells a command into a running
// container rather than mutating Docker's own state, so it always runs.
func dockerExecCmd(get func() (docker.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <name> -- <args...>",
		Short: "Run a command inside a container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := get()
			if err != nil {
				return err
			}
			v, err := c.Exec(cmd.Context(), args[0], args[1:]...)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
}
