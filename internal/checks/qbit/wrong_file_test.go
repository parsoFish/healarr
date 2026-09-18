package qbit

import (
	"context"
	"errors"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/config"
)

// filesFailQBit delegates to an embedded *qbittorrent.Fake for everything
// except Files, which fails for the hashes in failHashes (or for every
// hash when failHashes is nil). qbittorrent.Fake's single Err field fails
// every method uniformly, so it can't express "Torrents succeeds, Files
// fails for one torrent" on its own.
type filesFailQBit struct {
	*qbittorrent.Fake
	filesErr   error
	failHashes map[string]bool
}

func (f filesFailQBit) Files(ctx context.Context, hash string) ([]qbittorrent.File, error) {
	if f.failHashes != nil && !f.failHashes[hash] {
		return f.Fake.Files(ctx, hash)
	}
	f.Calls = append(f.Calls, "Files("+hash+")")
	return nil, f.filesErr
}

func TestWrongFileType(t *testing.T) {
	c := Checks(config.Config{})[2]
	cfg := testChecksCfg()

	tests := []struct {
		name     string
		torrents []qbittorrent.Torrent
		files    map[string][]qbittorrent.File
		want     []wantFinding
		wantErr  error
	}{
		{
			name:     "fake .exe episode",
			torrents: []qbittorrent.Torrent{{Hash: "AAA", Name: "Show.S01E01", Category: "tv"}},
			files: map[string][]qbittorrent.File{"AAA": {
				{Name: "Show.S01E01.mkv.exe"},
				{Name: "readme.txt"},
			}},
			want: []wantFinding{{key: "qbit:AAA", sev: check.SeverityCritical}},
		},
		{
			name:     "clean tv torrent",
			torrents: []qbittorrent.Torrent{{Hash: "BBB", Name: "Clean Show", Category: "tv"}},
			files: map[string][]qbittorrent.File{"BBB": {
				{Name: "Show.S01E01.mkv"},
				{Name: "Show.S01E01.nfo"},
			}},
			want: nil,
		},
		{
			name:     "iso in tv category is disallowed",
			torrents: []qbittorrent.Torrent{{Hash: "CCC", Name: "Fake Season Pack", Category: "tv"}},
			files:    map[string][]qbittorrent.File{"CCC": {{Name: "season01.iso"}}},
			want:     []wantFinding{{key: "qbit:CCC", sev: check.SeverityCritical}},
		},
		{
			// qBittorrent categories are free text, so a user who typed
			// "TV" must get the same rule as one who typed "tv".
			name:     "iso in differently-cased tv category is disallowed",
			torrents: []qbittorrent.Torrent{{Hash: "CCU", Name: "Fake Season Pack", Category: "TV"}},
			files:    map[string][]qbittorrent.File{"CCU": {{Name: "season01.iso"}}},
			want:     []wantFinding{{key: "qbit:CCU", sev: check.SeverityCritical}},
		},
		{
			name:     "iso in movies category is allowed",
			torrents: []qbittorrent.Torrent{{Hash: "DDD", Name: "Movie Disc", Category: "movies"}},
			files:    map[string][]qbittorrent.File{"DDD": {{Name: "movie.iso"}}},
			want:     nil,
		},
		{
			name:     "multiple disallowed files in one torrent",
			torrents: []qbittorrent.Torrent{{Hash: "EEE", Name: "Multi Bad", Category: "tv"}},
			files: map[string][]qbittorrent.File{"EEE": {
				{Name: "a.exe"}, {Name: "b.scr"}, {Name: "c.mkv"},
			}},
			want: []wantFinding{{key: "qbit:EEE", sev: check.SeverityCritical}},
		},
		{
			name:    "not configured",
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var qbit qbittorrent.Client
			if tt.name != "not configured" {
				qbit = &qbittorrent.Fake{TorrentList: tt.torrents, FilesByHash: tt.files}
			}
			run(t, c, baseDeps(cfg, qbit, nil, nil), tt.want, tt.wantErr)
		})
	}
}

func TestWrongFileTypeTorrentsError(t *testing.T) {
	c := Checks(config.Config{})[2]
	cfg := testChecksCfg()
	qbit := &qbittorrent.Fake{Err: errors.New("boom")}
	run(t, c, baseDeps(cfg, qbit, nil, nil), nil, errAny)
}

// TestWrongFileTypeFilesErrorIsTolerated proves one torrent qBittorrent
// cannot describe degrades to a single warn finding rather than failing
// the whole check: the other torrents still get inspected.
func TestWrongFileTypeFilesErrorIsTolerated(t *testing.T) {
	c := Checks(config.Config{})[2]
	cfg := testChecksCfg()
	qbit := filesFailQBit{
		Fake:     &qbittorrent.Fake{TorrentList: []qbittorrent.Torrent{{Hash: "AAA", Name: "Any", Category: "tv"}}},
		filesErr: errors.New("files unavailable"),
	}
	res := run(t, c, baseDeps(cfg, qbit, nil, nil),
		[]wantFinding{{key: inspectErrorsKey, sev: check.SeverityWarn}}, nil)

	f := res.Findings[0]
	if f.Tier != check.TierObserve {
		t.Errorf("tier = %s, want %s", f.Tier, check.TierObserve)
	}
	if want := "1 torrent(s) could not be inspected"; f.Summary != want {
		t.Errorf("summary = %q, want %q", f.Summary, want)
	}
	hashes, _ := f.Data["hashes"].([]string)
	if len(hashes) != 1 || hashes[0] != "AAA" {
		t.Errorf("Data[hashes] = %v, want [AAA]", f.Data["hashes"])
	}
	if got := f.Data["firstError"]; got != "files unavailable" {
		t.Errorf("Data[firstError] = %v, want %q", got, "files unavailable")
	}
	if got := res.Metrics["qbit_wrong_file_inspect_errors"]; got != 1 {
		t.Errorf("qbit_wrong_file_inspect_errors = %v, want 1", got)
	}
}

// TestWrongFileTypeMixedErrorAndFinding proves an uninspectable torrent
// does not hide a real one: the .exe in the second torrent is still
// reported alongside the inspect-errors warning.
func TestWrongFileTypeMixedErrorAndFinding(t *testing.T) {
	c := Checks(config.Config{})[2]
	cfg := testChecksCfg()
	qbit := filesFailQBit{
		Fake: &qbittorrent.Fake{
			TorrentList: []qbittorrent.Torrent{
				{Hash: "AAA", Name: "Unreadable", Category: "tv"},
				{Hash: "BBB", Name: "Fake Release", Category: "tv"},
			},
			FilesByHash: map[string][]qbittorrent.File{"BBB": {{Name: "Show.S01E01.mkv.exe"}}},
		},
		filesErr:   errors.New("files unavailable"),
		failHashes: map[string]bool{"AAA": true},
	}
	res := run(t, c, baseDeps(cfg, qbit, nil, nil), []wantFinding{
		{key: "qbit:BBB", sev: check.SeverityCritical},
		{key: inspectErrorsKey, sev: check.SeverityWarn},
	}, nil)

	if got := res.Metrics["qbit_wrong_file_inspect_errors"]; got != 1 {
		t.Errorf("qbit_wrong_file_inspect_errors = %v, want 1", got)
	}
	if got := res.Metrics["qbit_wrong_file_torrents"]; got != 1 {
		t.Errorf("qbit_wrong_file_torrents = %v, want 1 (the inspect-errors finding must not be counted)", got)
	}
}

// TestWrongFileTypeFixtureDetail pins the "fake .exe episode" fixture's
// exact summary and Data payload.
func TestWrongFileTypeFixtureDetail(t *testing.T) {
	c := Checks(config.Config{})[2]
	cfg := testChecksCfg()
	torrents := []qbittorrent.Torrent{{Hash: "AAA", Name: "Show.S01E01", Category: "tv"}}
	files := map[string][]qbittorrent.File{"AAA": {
		{Name: "Show.S01E01.mkv.exe"},
		{Name: "readme.txt"},
	}}
	res := run(t, c, baseDeps(cfg, &qbittorrent.Fake{TorrentList: torrents, FilesByHash: files}, nil, nil),
		[]wantFinding{{key: "qbit:AAA", sev: check.SeverityCritical}}, nil)

	f := res.Findings[0]
	wantSummary := "Show.S01E01 contains 1 disallowed file(s): Show.S01E01.mkv.exe"
	if f.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", f.Summary, wantSummary)
	}
	gotFiles, _ := f.Data["files"].([]string)
	if len(gotFiles) != 1 || gotFiles[0] != "Show.S01E01.mkv.exe" {
		t.Errorf("Data[files] = %v, want [Show.S01E01.mkv.exe]", gotFiles)
	}
	if got := f.Data["category"]; got != "tv" {
		t.Errorf("Data[category] = %v, want tv", got)
	}
}

// TestWrongFileTypeMetrics checks qbit_wrong_file_torrents counts torrents
// with at least one disallowed file, not the number of disallowed files.
func TestWrongFileTypeMetrics(t *testing.T) {
	c := Checks(config.Config{})[2]
	cfg := testChecksCfg()
	torrents := []qbittorrent.Torrent{
		{Hash: "AAA", Name: "Bad One", Category: "tv"},
		{Hash: "BBB", Name: "Bad Two", Category: "tv"},
		{Hash: "CCC", Name: "Clean", Category: "tv"},
	}
	files := map[string][]qbittorrent.File{
		"AAA": {{Name: "a.exe"}},
		"BBB": {{Name: "b.scr"}, {Name: "c.msi"}},
		"CCC": {{Name: "clean.mkv"}},
	}
	res := run(t, c, baseDeps(cfg, &qbittorrent.Fake{TorrentList: torrents, FilesByHash: files}, nil, nil),
		[]wantFinding{{key: "qbit:AAA", sev: check.SeverityCritical}, {key: "qbit:BBB", sev: check.SeverityCritical}}, nil)
	if got := res.Metrics["qbit_wrong_file_torrents"]; got != 2 {
		t.Errorf("qbit_wrong_file_torrents = %v, want 2", got)
	}
	if _, ok := res.Metrics["qbit_wrong_file_inspect_errors"]; ok {
		t.Errorf("qbit_wrong_file_inspect_errors should be absent when every torrent was inspected")
	}
}
