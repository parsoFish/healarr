package disk

import (
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/config"
)

func TestOrphanDownloads(t *testing.T) {
	c := Checks(config.Config{})[5] // orphan_downloads
	cfg := config.Config{Checks: config.Checks{DownloadsDirs: []string{"/downloads"}, OrphanAfter: 7 * 24 * time.Hour}}
	old := fixedNow.Add(-10 * 24 * time.Hour)
	fresh := fixedNow.Add(-1 * time.Hour)

	// tracked is a torrent whose ContentPath and SavePath/Name cover a
	// direct file entry.
	tracked := qbittorrent.Torrent{Name: "Show.S01E01", SavePath: "/downloads", ContentPath: "/downloads/Show.S01E01.mkv"}
	// packTorrent's SavePath/Name deliberately do NOT resolve to its
	// content folder ("/downloads/Movie.Pack"), so the folder entry is
	// covered only via the parent-of-ContentPath rule, not via the
	// SavePath/Name pairing — isolating that branch from the other one.
	packTorrent := qbittorrent.Torrent{Name: "movie.mkv", SavePath: "/downloads/elsewhere", ContentPath: "/downloads/Movie.Pack/movie.mkv"}

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "tracked entry ignored",
			deps: baseDeps(cfg, &hostfs.Fake{Entries: map[string][]hostfs.Entry{
				"/downloads": {{Name: "Show.S01E01.mkv", Size: 10, ModTime: old}},
			}}, nil, &qbittorrent.Fake{TorrentList: []qbittorrent.Torrent{tracked}}),
			want: nil,
		},
		{
			name: "tracked content path parent dir ignored",
			deps: baseDeps(cfg, &hostfs.Fake{Entries: map[string][]hostfs.Entry{
				"/downloads": {{Name: "Movie.Pack", IsDir: true, ModTime: old}},
			}}, nil, &qbittorrent.Fake{TorrentList: []qbittorrent.Torrent{packTorrent}}),
			want: nil,
		},
		{
			name: "untracked and old is flagged",
			deps: baseDeps(cfg, &hostfs.Fake{Entries: map[string][]hostfs.Entry{
				"/downloads": {{Name: "Orphan.File.mkv", Size: 123, ModTime: old}},
			}}, nil, &qbittorrent.Fake{TorrentList: []qbittorrent.Torrent{tracked}}),
			want: []wantFinding{{key: "/downloads/Orphan.File.mkv", sev: check.SeverityWarn}},
		},
		{
			name: "untracked but fresh is ignored",
			deps: baseDeps(cfg, &hostfs.Fake{Entries: map[string][]hostfs.Entry{
				"/downloads": {{Name: "StillDownloading.mkv", ModTime: fresh}},
			}}, nil, &qbittorrent.Fake{TorrentList: []qbittorrent.Torrent{tracked}}),
			want: nil,
		},
		{
			name:    "qbit error",
			deps:    baseDeps(cfg, &hostfs.Fake{}, nil, &qbittorrent.Fake{Err: errors.New("qbit unreachable")}),
			wantErr: errAny,
		},
		{
			name:    "listdir error",
			deps:    baseDeps(cfg, &hostfs.Fake{Err: errors.New("permission denied")}, nil, &qbittorrent.Fake{}),
			wantErr: errAny,
		},
		{
			name:    "not configured: no dirs",
			deps:    baseDeps(config.Config{}, &hostfs.Fake{}, nil, &qbittorrent.Fake{}),
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "not configured: no host client",
			deps:    baseDeps(cfg, nil, nil, &qbittorrent.Fake{}),
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "not configured: no qbit client",
			deps:    baseDeps(cfg, &hostfs.Fake{}, nil, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, c, tt.deps, tt.want, tt.wantErr)
			if tt.name == "untracked and old is flagged" {
				f := res.Findings[0]
				if f.Tier != check.TierCorrect {
					t.Fatalf("Tier = %s, want %s", f.Tier, check.TierCorrect)
				}
				if f.Data["size"] != int64(123) || f.Data["isDir"] != false {
					t.Fatalf("Data = %+v", f.Data)
				}
				if got := res.Metrics["orphan_downloads_count"]; got != 1 {
					t.Fatalf("orphan_downloads_count metric = %v, want 1", got)
				}
			}
		})
	}
}
