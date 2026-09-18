package qbit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

func historyDeps(snr sonarr.Client, rdr radarr.Client) check.Deps {
	return check.Deps{
		Node:   config.NodeNAS,
		Now:    func() time.Time { return fixedNow },
		Sonarr: snr,
		Radarr: rdr,
	}
}

func TestImportedDownloadIDsUnionsSonarrAndRadarr(t *testing.T) {
	since := fixedNow.Add(-24 * time.Hour)
	snr := &sonarr.Fake{HistoryRecords: []sonarr.HistoryRecord{
		{DownloadID: "aaa", EventType: "downloadFolderImported", Date: fixedNow.Add(-time.Hour)},
		{DownloadID: "bbb", EventType: "grabbed", Date: fixedNow.Add(-time.Hour)}, // wrong event type, excluded
	}}
	rdr := &radarr.Fake{HistoryRecords: []radarr.HistoryRecord{
		{DownloadID: "ccc", EventType: "downloadFolderImported", Date: fixedNow.Add(-time.Hour)},
	}}

	ids, err := importedDownloadIDs(context.Background(), historyDeps(snr, rdr), since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]bool{"AAA": true, "CCC": true}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for k := range want {
		if !ids[k] {
			t.Errorf("missing id %s in %v", k, ids)
		}
	}
}

func TestImportedDownloadIDsSkipsNilClients(t *testing.T) {
	since := fixedNow.Add(-24 * time.Hour)
	rdr := &radarr.Fake{HistoryRecords: []radarr.HistoryRecord{
		{DownloadID: "ccc", EventType: "downloadFolderImported", Date: fixedNow.Add(-time.Hour)},
	}}

	ids, err := importedDownloadIDs(context.Background(), historyDeps(nil, rdr), since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || !ids["CCC"] {
		t.Fatalf("ids = %v, want {CCC: true}", ids)
	}

	ids, err = importedDownloadIDs(context.Background(), historyDeps(nil, nil), since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
}

func TestImportedDownloadIDsWrapsSonarrError(t *testing.T) {
	wantErr := errors.New("sonarr down")
	snr := &sonarr.Fake{Err: wantErr}
	_, err := importedDownloadIDs(context.Background(), historyDeps(snr, nil), fixedNow.Add(-time.Hour))
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want errors.Is(%v)", err, wantErr)
	}
}

func TestImportedDownloadIDsWrapsRadarrError(t *testing.T) {
	wantErr := errors.New("radarr down")
	rdr := &radarr.Fake{Err: wantErr}
	_, err := importedDownloadIDs(context.Background(), historyDeps(&sonarr.Fake{}, rdr), fixedNow.Add(-time.Hour))
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want errors.Is(%v)", err, wantErr)
	}
}

func TestImportedDownloadIDsSkipsEmptyDownloadID(t *testing.T) {
	snr := &sonarr.Fake{HistoryRecords: []sonarr.HistoryRecord{
		{DownloadID: "", EventType: "downloadFolderImported", Date: fixedNow.Add(-time.Hour)},
	}}
	ids, err := importedDownloadIDs(context.Background(), historyDeps(snr, nil), fixedNow.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
}
