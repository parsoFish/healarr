package hostfs

import (
	"context"
	"fmt"
	"os"
)

// Remove deletes path and everything under it via os.RemoveAll. It refuses
// an empty path or "/" outright, rather than trusting a caller-computed
// path never collapses to the filesystem root; every other "is this
// actually a directory we're allowed to delete from" check is the
// caller's responsibility (see internal/cleanup).
func (o *OS) Remove(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" || path == "/" {
		return fmt.Errorf("hostfs: refusing to remove %q", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("hostfs: remove %s: %w", path, err)
	}
	return nil
}
