package plex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/plex"
	"github.com/parsoFish/healarr/internal/config"
)

const plexScanFreshnessID = "plex_scan_freshness"

// plexScanFreshnessCheck watches that each configured Plex library has been
// rescanned recently enough to reflect the newest file sitting in its host
// directory: a scan that silently stopped running (a crashed inotify
// watcher, a library Plex lost track of) otherwise goes unnoticed until
// someone happens to look for a file Plex never picked up.
func plexScanFreshnessCheck() check.Check {
	return check.Check{
		ID:      plexScanFreshnessID,
		Nodes:   check.NASOnly,
		Tier:    check.TierNudge,
		Cadence: check.Daily,
		Run:     runPlexScanFreshness,
	}
}

func runPlexScanFreshness(ctx context.Context, d check.Deps) (check.Result, error) {
	libraries := d.Cfg.Checks.PlexLibraries
	if d.Plex == nil || d.Host == nil || len(libraries) == 0 {
		return check.Result{}, check.ErrNotConfigured
	}

	plexLibs, err := d.Plex.Libraries(ctx)
	if err != nil {
		return check.Result{}, fmt.Errorf("libraries: %w", err)
	}

	var findings []check.Finding
	metrics := map[string]float64{}
	for _, configured := range libraries {
		f, err := scanFreshnessFinding(ctx, d, configured, plexLibs, metrics)
		if err != nil {
			return check.Result{}, err
		}
		if f != nil {
			findings = append(findings, *f)
		}
	}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}

// scanFreshnessFinding applies the plex_scan_freshness rule to one
// configured library: a title Plex no longer reports is a warn on its own;
// otherwise the host directory's newest file modification time is compared
// against Plex's last scan of that library, and the gap is recorded as a
// metric regardless of whether it crosses the stale threshold.
func scanFreshnessFinding(ctx context.Context, d check.Deps, configured config.PlexLibrary, plexLibs []plex.Library, metrics map[string]float64) (*check.Finding, error) {
	lib, ok := findLibraryByTitle(plexLibs, configured.Title)
	if !ok {
		f := d.NewFinding(plexScanFreshnessID, "plex:"+configured.Title, check.SeverityWarn, check.TierNudge,
			fmt.Sprintf("plex library %s not found", configured.Title))
		return &f, nil
	}

	entries, err := d.Host.ListDir(ctx, configured.Path)
	if err != nil {
		return nil, fmt.Errorf("listdir %s: %w", configured.Path, err)
	}

	newest := newestModTime(entries)
	age := newest.Sub(lib.ScannedAt)
	metrics["plex_scan_age_hours:"+lib.Key] = age.Hours()

	if age <= d.Cfg.Checks.PlexScanStaleAfter {
		return nil, nil
	}

	f := d.NewFinding(plexScanFreshnessID, "plex:"+lib.Key, check.SeverityWarn, check.TierNudge,
		fmt.Sprintf("plex library %s last scanned %s but %s changed %s",
			configured.Title, lib.ScannedAt.Format(time.RFC3339), configured.Path, newest.Format(time.RFC3339)))
	return &f, nil
}

// findLibraryByTitle finds the Plex library named title, case-insensitive.
func findLibraryByTitle(libs []plex.Library, title string) (plex.Library, bool) {
	for _, l := range libs {
		if strings.EqualFold(l.Title, title) {
			return l, true
		}
	}
	return plex.Library{}, false
}

// newestModTime returns the latest ModTime among entries, or the zero
// time.Time for an empty directory.
func newestModTime(entries []hostfs.Entry) time.Time {
	var newest time.Time
	for _, e := range entries {
		if e.ModTime.After(newest) {
			newest = e.ModTime
		}
	}
	return newest
}
