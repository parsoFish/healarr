package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/docker"
)

func newDockerCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	get := newGetter(deps, flags, "docker", deps.Docker)
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

// dockerExecCmd shells a command into a running container. It can run
// arbitrary commands with side effects inside that container, so it honours
// --dry-run like every other write verb rather than always running.
func dockerExecCmd(get func() (docker.Client, error), flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <name> -- <args...>",
		Short: "Run a command inside a container (W)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			container, execArgs := args[0], args[1:]
			desc := fmt.Sprintf("Exec(%s, %s)", container, strings.Join(execArgs, " "))
			return doOrDryRun(cmd, flags, desc, func() (any, error) {
				c, err := get()
				if err != nil {
					return nil, err
				}
				return c.Exec(cmd.Context(), container, execArgs...)
			})
		},
	}
}
