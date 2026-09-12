package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/clients/hostfs"
)

// hostGetter returns a lazy client factory: it loads config on first use
// (never at registration time), so `healarr version` never touches disk.
func hostGetter(deps *Deps, flags *GlobalFlags) func() (hostfs.Client, error) {
	return func() (hostfs.Client, error) {
		cfg, sec, err := deps.load(flags)
		if err != nil {
			return nil, err
		}
		return deps.Host(cfg, sec)
	}
}

func newHostCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	get := hostGetter(deps, flags)
	root := &cobra.Command{Use: "host", Short: "Host filesystem mounts, devices and disk usage"}
	root.AddCommand(
		simpleCmd("mounts", "Kernel mount table", get, flags,
			func(ctx context.Context, c hostfs.Client) (any, error) { return c.Mounts(ctx) }),
		hostPathCmd("ismount <path>", "Report whether path is a mount point", get, flags,
			func(ctx context.Context, c hostfs.Client, path string) (any, error) {
				v, err := c.IsMountpoint(ctx, path)
				if err != nil {
					return nil, err
				}
				return map[string]any{"mountpoint": v}, nil
			}),
		hostPathCmd("dev <path>", "Report path's device id", get, flags,
			func(ctx context.Context, c hostfs.Client, path string) (any, error) {
				v, err := c.DeviceID(ctx, path)
				if err != nil {
					return nil, err
				}
				return map[string]any{"device_id": v}, nil
			}),
		hostPathCmd("usage <path>", "Space usage for path's filesystem", get, flags,
			func(ctx context.Context, c hostfs.Client, path string) (any, error) { return c.Usage(ctx, path) }),
		hostPathCmd("ls <path>", "List a directory's immediate children", get, flags,
			func(ctx context.Context, c hostfs.Client, path string) (any, error) { return c.ListDir(ctx, path) }),
		hostDirsizeCmd(get, flags),
	)
	return root
}

// hostPathCmd builds a read-only command taking a single path argument.
func hostPathCmd(use, short string, get func() (hostfs.Client, error), flags *GlobalFlags,
	call func(context.Context, hostfs.Client, string) (any, error),
) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := get()
			if err != nil {
				return err
			}
			v, err := call(cmd.Context(), c, args[0])
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, v)
		},
	}
}

func hostDirsizeCmd(get func() (hostfs.Client, error), flags *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dirsize <path>",
		Short: "Sum regular file sizes under path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			depth, _ := cmd.Flags().GetInt("depth")
			c, err := get()
			if err != nil {
				return err
			}
			v, err := c.DirSize(cmd.Context(), args[0], depth)
			if err != nil {
				return err
			}
			return Print(cmd.OutOrStdout(), flags.JSON, map[string]any{"bytes": v})
		},
	}
	cmd.Flags().Int("depth", 0, "maximum directory depth to descend (0 = unlimited)")
	return cmd
}
