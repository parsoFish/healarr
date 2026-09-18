package qbit

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
)

const wrongFileTypeID = "wrong_file_type"

// wrongFileTypeCheck flags torrents that contain a disallowed file type:
// an executable, script or installer masquerading as media (the classic
// "Show.S01E01.mkv.exe" fake release), or an ISO image sitting in a TV
// category where it can only ever be junk.
func wrongFileTypeCheck() check.Check {
	return check.Check{
		ID:      wrongFileTypeID,
		Nodes:   check.NASOnly,
		Tier:    check.TierCorrect,
		Cadence: check.Every15m,
		Run:     runWrongFileType,
	}
}

func runWrongFileType(ctx context.Context, d check.Deps) (check.Result, error) {
	if d.QBit == nil {
		return check.Result{}, check.ErrNotConfigured
	}
	torrents, err := d.QBit.Torrents(ctx, "", "")
	if err != nil {
		return check.Result{}, fmt.Errorf("qbit torrents: %w", err)
	}

	var findings []check.Finding
	for _, t := range torrents {
		files, err := d.QBit.Files(ctx, t.Hash)
		if err != nil {
			return check.Result{}, fmt.Errorf("qbit files %s: %w", t.Hash, err)
		}
		if f := wrongFileFinding(d, t, files); f != nil {
			findings = append(findings, *f)
		}
	}
	metrics := map[string]float64{"qbit_wrong_file_torrents": float64(len(findings))}
	return check.Result{Findings: findings, Metrics: metrics}, nil
}

// wrongFileFinding inspects every file in one torrent and, if any are
// disallowed, returns a single finding naming them all.
func wrongFileFinding(d check.Deps, t qbittorrent.Torrent, files []qbittorrent.File) *check.Finding {
	var bad []string
	for _, file := range files {
		if isBadFile(file.Name, t.Category, d.Cfg.Checks.WrongFileExts, d.Cfg.Checks.TVCategories) {
			bad = append(bad, file.Name)
		}
	}
	if len(bad) == 0 {
		return nil
	}

	f := d.NewFinding(wrongFileTypeID, "qbit:"+t.Hash, check.SeverityCritical, check.TierCorrect,
		fmt.Sprintf("%s contains %d disallowed file(s): %s", t.Name, len(bad), bad[0]))
	f.Data = map[string]any{"files": bad, "category": t.Category}
	return &f
}

// isBadFile reports whether name is disallowed for a torrent in category:
// its extension is in wrongExts outright, or it's a .iso file in a TV
// category (installer discs and season packs disguised as ISOs are the
// classic fake-release shape there; a movie ISO is legitimate).
func isBadFile(name, category string, wrongExts, tvCategories []string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if extInList(ext, wrongExts) {
		return true
	}
	return ext == ".iso" && stringInList(category, tvCategories)
}

func extInList(ext string, exts []string) bool {
	for _, e := range exts {
		if strings.ToLower(e) == ext {
			return true
		}
	}
	return false
}

func stringInList(s string, list []string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
