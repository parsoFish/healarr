package cleanup

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// executeHostRemove is the shared executor for the recycle and orphans
// kinds: both remove a host path via check.Deps.Host. Every item's Key is
// re-validated against the kind's configured directory prefixes here —
// defence in depth, independent of whatever the planner already filtered
// by, so a plan built against stale or tampered config still can't walk a
// removal outside the directories the operator actually named.
func executeHostRemove(ctx context.Context, d check.Deps, p Plan) (Result, error) {
	if d.Host == nil {
		return Result{}, fmt.Errorf("cleanup: %s: host filesystem client not configured", p.Kind)
	}
	prefixes, err := allowedPrefixes(d.Cfg, p.Kind)
	if err != nil {
		return Result{}, err
	}

	res := Result{Kind: p.Kind}
	for _, item := range p.Items {
		if !underAnyPrefix(item.Key, prefixes) {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: not under a configured directory, refusing to remove", item.Key))
			continue
		}
		if err := d.Host.Remove(ctx, item.Key); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", item.Key, err))
			continue
		}
		res.Executed++
		res.Bytes += item.Bytes
	}
	return res, nil
}

// allowedPrefixes returns the configured directories a Key of kind must
// sit under to be eligible for removal.
func allowedPrefixes(cfg config.Config, kind Kind) ([]string, error) {
	switch kind {
	case KindRecycle:
		return cfg.Checks.RecycleDirs, nil
	case KindOrphans:
		return cfg.Checks.DownloadsDirs, nil
	default:
		return nil, fmt.Errorf("cleanup: %s does not remove host paths", kind)
	}
}

// underAnyPrefix reports whether path is exactly one of prefixes, or
// nested under one of them.
func underAnyPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if isUnderDir(prefix, path) {
			return true
		}
	}
	return false
}

// isUnderDir reports whether path is dir itself or nested under it, after
// cleaning both. A naive strings.HasPrefix(path, dir) would wrongly match
// "/data/recycle-old" against dir "/data/recycle", so the comparison
// requires either exact equality or a path separator right after dir.
func isUnderDir(dir, path string) bool {
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	if dir == path {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}
