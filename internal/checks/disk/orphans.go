package disk

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
)

const orphanDownloadsID = "orphan_downloads"

// orphanDownloadsCheck flags files and directories sitting in a downloads
// directory that qBittorrent no longer has any record of: a torrent
// removed from qBittorrent without "delete files", or content left behind
// by a client-side bug. It never reports without first confirming
// qBittorrent's own view — a client error there fails the whole check
// rather than risking false orphans — and it never deletes anything itself
// (see the family's pure-checks rule).
func orphanDownloadsCheck() check.Check {
	return check.Check{
		ID:      orphanDownloadsID,
		Nodes:   check.NASOnly,
		Tier:    check.TierCorrect,
		Cadence: check.Daily,
		Run:     runOrphanDownloads,
	}
}

func runOrphanDownloads(ctx context.Context, d check.Deps) (check.Result, error) {
	orphans, err := FindOrphans(ctx, d)
	if err != nil {
		return check.Result{}, err
	}

	var findings []check.Finding
	for _, o := range orphans {
		if d.Now().Sub(o.ModTime) <= d.Cfg.Checks.OrphanAfter {
			continue
		}
		f := d.NewFinding(orphanDownloadsID, o.Path, check.SeverityWarn, check.TierCorrect,
			fmt.Sprintf("%s is not tracked by qBittorrent (modified %s)", o.Path, o.ModTime.Format(time.DateOnly)))
		f.Data = map[string]any{"size": o.Size, "isDir": o.IsDir}
		findings = append(findings, f)
	}
	metrics := map[string]float64{"orphan_downloads_count": float64(len(findings))}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}

// Orphan is one file or directory sitting in a configured downloads
// directory that qBittorrent no longer accounts for, with no age filter
// applied — FindOrphans reports every untracked entry it finds; callers
// decide what "old enough" means for their own purpose (the
// orphan_downloads check uses Checks.OrphanAfter; the cleanup package's
// orphan planner uses Cleanup.OrphanMinAge).
type Orphan struct {
	Path    string
	Size    int64
	IsDir   bool
	ModTime time.Time
}

// FindOrphans lists every entry across d.Cfg.Checks.DownloadsDirs that
// qBittorrent's own torrent list doesn't account for (see trackedPaths).
// It is the rule shared by the orphan_downloads check and the cleanup
// package's orphan planner; extracting it here changes neither the
// check's behaviour nor its tests.
func FindOrphans(ctx context.Context, d check.Deps) ([]Orphan, error) {
	dirs := d.Cfg.Checks.DownloadsDirs
	if d.Host == nil || d.QBit == nil || len(dirs) == 0 {
		return nil, check.ErrNotConfigured
	}

	torrents, err := d.QBit.Torrents(ctx, "", "")
	if err != nil {
		return nil, fmt.Errorf("qbit torrents: %w", err)
	}
	known, knownParents := trackedPaths(torrents)

	var orphans []Orphan
	for _, dir := range dirs {
		entries, err := d.Host.ListDir(ctx, dir)
		if err != nil {
			return nil, fmt.Errorf("listdir %s: %w", dir, err)
		}
		orphans = append(orphans, orphanEntries(dir, entries, known, knownParents)...)
	}
	return orphans, nil
}

// trackedPaths builds the set of paths qBittorrent accounts for: each
// torrent's ContentPath and its SavePath/Name, plus — separately — the
// parent directory of ContentPath. A multi-file torrent's own folder shows
// up as one entry when its downloads dir is listed, and is tracked via its
// files' shared ContentPath rather than having a ContentPath of its own.
func trackedPaths(torrents []qbittorrent.Torrent) (known, knownParents map[string]bool) {
	known = make(map[string]bool, len(torrents)*2)
	knownParents = make(map[string]bool, len(torrents))
	for _, t := range torrents {
		if t.ContentPath != "" {
			known[filepath.Clean(t.ContentPath)] = true
			knownParents[filepath.Dir(t.ContentPath)] = true
		}
		if t.SavePath != "" && t.Name != "" {
			known[filepath.Join(t.SavePath, t.Name)] = true
		}
	}
	return known, knownParents
}

// orphanEntries turns one directory's listing into Orphans: an entry
// qBittorrent doesn't account for. No age filter is applied here — that's
// each caller's own call (see FindOrphans's doc comment).
func orphanEntries(dir string, entries []hostfs.Entry, known, knownParents map[string]bool) []Orphan {
	var orphans []Orphan
	for _, e := range entries {
		full := filepath.Join(dir, e.Name)
		if known[full] || knownParents[full] {
			continue
		}
		orphans = append(orphans, Orphan{Path: full, Size: e.Size, IsDir: e.IsDir, ModTime: e.ModTime})
	}
	return orphans
}
