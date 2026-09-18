package cleanup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/config"
)

func orphansCfg() config.Config {
	return config.Config{
		Checks:  config.Checks{DownloadsDirs: []string{"/downloads"}},
		Cleanup: config.Cleanup{OrphanMinAge: 168 * time.Hour},
	}
}

func TestOrphansPlanNotConfigured(t *testing.T) {
	p := orphansPlanner{}
	d := baseDeps(config.Config{}, withHost(&hostfs.Fake{}), withQBit(&qbittorrent.Fake{}))
	if _, err := p.Plan(context.Background(), d); !errors.Is(err, check.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestOrphansPlanUsesCleanupOrphanMinAgeNotChecksOrphanAfter(t *testing.T) {
	// old clears Cleanup.OrphanMinAge (168h) but Checks.OrphanAfter is left
	// at its zero value, proving the planner reads its own, more
	// conservative threshold rather than the check's.
	old := fixedNow.Add(-200 * time.Hour)
	tooYoung := fixedNow.Add(-24 * time.Hour)
	host := &hostfs.Fake{Entries: map[string][]hostfs.Entry{
		"/downloads": {
			{Name: "orphan-old.mkv", Size: 500, ModTime: old},
			{Name: "orphan-young.mkv", Size: 50, ModTime: tooYoung},
		},
	}}
	d := baseDeps(orphansCfg(), withHost(host), withQBit(&qbittorrent.Fake{}))

	plan, err := (orphansPlanner{}).Plan(context.Background(), d)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Key != "/downloads/orphan-old.mkv" {
		t.Fatalf("Items = %+v, want only orphan-old.mkv", plan.Items)
	}
	if plan.Bytes != 500 {
		t.Errorf("plan.Bytes = %d, want 500", plan.Bytes)
	}
}

func TestOrphansPlanPropagatesFindOrphansError(t *testing.T) {
	host := &hostfs.Fake{Err: errors.New("listdir boom")}
	d := baseDeps(orphansCfg(), withHost(host), withQBit(&qbittorrent.Fake{}))
	if _, err := (orphansPlanner{}).Plan(context.Background(), d); err == nil {
		t.Fatal("expected FindOrphans's error to propagate")
	}
}

func TestOrphansExecuteRefusesPathsOutsideDownloadsDirs(t *testing.T) {
	host := &hostfs.Fake{}
	cfg := enabledCfg(func(c *config.Config) { c.Checks.DownloadsDirs = []string{"/downloads"} })
	d := baseDeps(cfg, withHost(host))

	plan := Plan{Kind: KindOrphans, Items: []PlanItem{
		{Key: "/downloads/orphan.mkv", Bytes: 10},
		{Key: "/root/.ssh/id_rsa", Bytes: 1},
	}}
	res, err := Execute(context.Background(), d, plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Executed != 1 || len(res.Errors) != 1 {
		t.Fatalf("Result = %+v, want Executed=1 and one refusal", res)
	}
	if len(host.Removed) != 1 || host.Removed[0] != "/downloads/orphan.mkv" {
		t.Fatalf("Removed = %v", host.Removed)
	}
}
