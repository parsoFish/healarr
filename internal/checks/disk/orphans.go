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
	dirs := d.Cfg.Checks.DownloadsDirs
	if d.Host == nil || d.QBit == nil || len(dirs) == 0 {
		return check.Result{}, check.ErrNotConfigured
	}

	torrents, err := d.QBit.Torrents(ctx, "", "")
	if err != nil {
		return check.Result{}, fmt.Errorf("qbit torrents: %w", err)
	}
	known, knownParents := trackedPaths(torrents)

	var findings []check.Finding
	for _, dir := range dirs {
		entries, err := d.Host.ListDir(ctx, dir)
		if err != nil {
			return check.Result{}, fmt.Errorf("listdir %s: %w", dir, err)
		}
		findings = append(findings, orphansInDir(d, dir, entries, known, knownParents)...)
	}
	metrics := map[string]float64{"orphan_downloads_count": float64(len(findings))}
	return check.Result{Findings: findings, Metrics: metrics}, nil
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

// orphansInDir applies the orphan_downloads rule to one directory's
// listing: an entry qBittorrent doesn't account for, old enough that it
// isn't just a torrent still being written, is an orphan.
func orphansInDir(d check.Deps, dir string, entries []hostfs.Entry, known, knownParents map[string]bool) []check.Finding {
	var findings []check.Finding
	for _, e := range entries {
		full := filepath.Join(dir, e.Name)
		if known[full] || knownParents[full] {
			continue
		}
		if d.Now().Sub(e.ModTime) <= d.Cfg.Checks.OrphanAfter {
			continue
		}
		f := d.NewFinding(orphanDownloadsID, full, check.SeverityWarn, check.TierCorrect,
			fmt.Sprintf("%s is not tracked by qBittorrent (modified %s)", full, e.ModTime.Format(time.DateOnly)))
		f.Data = map[string]any{"size": e.Size, "isDir": e.IsDir}
		findings = append(findings, f)
	}
	return findings
}
