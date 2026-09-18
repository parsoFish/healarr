package cleanup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
)

func seededCfg() config.Config {
	return config.Config{Checks: config.Checks{SeededMinAge: 24 * time.Hour, ArrHistoryWindow: 7 * 24 * time.Hour}}
}

func TestSeededPlanNotConfigured(t *testing.T) {
	d := baseDeps(config.Config{})
	if _, err := (seededPlanner{}).Plan(context.Background(), d); !errors.Is(err, check.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestSeededPlanSumsMatchingTorrentSizes(t *testing.T) {
	imported := &sonarr.Fake{HistoryRecords: []sonarr.HistoryRecord{
		{DownloadID: "aaa", EventType: "downloadFolderImported", Date: fixedNow.Add(-2 * time.Hour)},
	}}
	qbit := &qbittorrent.Fake{
		TorrentList: []qbittorrent.Torrent{
			{Hash: "AAA", Name: "Done Show", Progress: 1, Ratio: 5, Size: 1_000, CompletionOn: fixedNow.Add(-48 * time.Hour)},
		},
		Prefs: qbittorrent.Preferences{MaxRatio: 2.0},
	}
	d := baseDeps(seededCfg(), withQBit(qbit), withSonarr(imported))

	plan, err := (seededPlanner{}).Plan(context.Background(), d)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Key != "AAA" || plan.Items[0].Bytes != 1000 {
		t.Fatalf("Items = %+v, want one AAA item of 1000 bytes", plan.Items)
	}
	if plan.Bytes != 1000 {
		t.Errorf("plan.Bytes = %d, want 1000", plan.Bytes)
	}
}

func TestSeededPlanPropagatesSeededDoneError(t *testing.T) {
	qbit := &qbittorrent.Fake{Err: errors.New("boom")}
	d := baseDeps(seededCfg(), withQBit(qbit), withSonarr(&sonarr.Fake{}))
	if _, err := (seededPlanner{}).Plan(context.Background(), d); err == nil {
		t.Fatal("expected SeededDone's error to propagate")
	}
}

func TestSeededExecuteNoClientErrors(t *testing.T) {
	d := baseDeps(enabledCfg(nil))
	if _, err := Execute(context.Background(), d, Plan{Kind: KindSeeded}); err == nil {
		t.Fatal("expected an error with no qbittorrent client configured")
	}
}

func TestSeededExecuteEmptyPlanIsNoOp(t *testing.T) {
	qbit := &qbittorrent.Fake{}
	d := baseDeps(enabledCfg(nil), withQBit(qbit))
	res, err := Execute(context.Background(), d, Plan{Kind: KindSeeded})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Executed != 0 || len(qbit.Calls) != 0 {
		t.Fatalf("Result = %+v, Calls = %v, want no Delete call for an empty plan", res, qbit.Calls)
	}
}

func TestSeededExecuteDeletesWithoutTouchingFiles(t *testing.T) {
	qbit := &qbittorrent.Fake{}
	d := baseDeps(enabledCfg(nil), withQBit(qbit))

	plan := Plan{Kind: KindSeeded, Bytes: 1500, Items: []PlanItem{
		{Key: "AAA", Bytes: 1000}, {Key: "BBB", Bytes: 500},
	}}
	res, err := Execute(context.Background(), d, plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Executed != 2 || res.Bytes != 1500 {
		t.Fatalf("Result = %+v, want Executed=2 Bytes=1500", res)
	}
	if len(qbit.Calls) != 1 || qbit.Calls[0] != "Delete([AAA BBB], false)" {
		t.Fatalf("Calls = %v, want Delete([AAA BBB], false)", qbit.Calls)
	}
}

func TestSeededExecuteDeleteErrorPropagates(t *testing.T) {
	qbit := &qbittorrent.Fake{Err: errors.New("delete boom")}
	d := baseDeps(enabledCfg(nil), withQBit(qbit))
	plan := Plan{Kind: KindSeeded, Items: []PlanItem{{Key: "AAA", Bytes: 1}}}
	if _, err := Execute(context.Background(), d, plan); err == nil {
		t.Fatal("expected Delete error to propagate")
	}
}
