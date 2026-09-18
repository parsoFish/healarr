package cleanup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/config"
)

func recycleCfg() config.Config {
	return config.Config{Checks: config.Checks{RecycleDirs: []string{"/recycle"}}, Cleanup: config.Cleanup{RecycleMinAge: 24 * time.Hour}}
}

func TestRecyclePlanNotConfigured(t *testing.T) {
	p := recyclePlanner{}

	t.Run("no host", func(t *testing.T) {
		if _, err := p.Plan(context.Background(), baseDeps(recycleCfg())); !errors.Is(err, check.ErrNotConfigured) {
			t.Fatalf("err = %v, want ErrNotConfigured", err)
		}
	})
	t.Run("no dirs", func(t *testing.T) {
		d := baseDeps(config.Config{}, withHost(&hostfs.Fake{}))
		if _, err := p.Plan(context.Background(), d); !errors.Is(err, check.ErrNotConfigured) {
			t.Fatalf("err = %v, want ErrNotConfigured", err)
		}
	})
}

func TestRecyclePlanFiltersByAgeAndSizesDirsRecursively(t *testing.T) {
	old := fixedNow.Add(-48 * time.Hour)
	fresh := fixedNow.Add(-1 * time.Hour)
	host := &hostfs.Fake{
		Entries: map[string][]hostfs.Entry{
			"/recycle": {
				{Name: "old-file.mkv", Size: 100, ModTime: old},
				{Name: "fresh-file.mkv", Size: 200, ModTime: fresh},
				{Name: "old-dir", IsDir: true, ModTime: old},
			},
		},
		Sizes: map[string]int64{"/recycle/old-dir": 9000},
	}
	d := baseDeps(recycleCfg(), withHost(host))

	plan, err := (recyclePlanner{}).Plan(context.Background(), d)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Items) != 2 {
		t.Fatalf("Items = %+v, want 2 (fresh-file excluded)", plan.Items)
	}
	byKey := map[string]PlanItem{}
	for _, it := range plan.Items {
		byKey[it.Key] = it
	}
	if got := byKey["/recycle/old-file.mkv"].Bytes; got != 100 {
		t.Errorf("old-file bytes = %d, want 100 (stat size)", got)
	}
	if got := byKey["/recycle/old-dir"].Bytes; got != 9000 {
		t.Errorf("old-dir bytes = %d, want 9000 (recursive DirSize)", got)
	}
	if plan.Bytes != 9100 {
		t.Errorf("plan.Bytes = %d, want 9100", plan.Bytes)
	}
	if plan.Kind != KindRecycle || plan.Node != config.NodeNAS {
		t.Errorf("plan Kind/Node = %s/%s", plan.Kind, plan.Node)
	}
}

func TestRecyclePlanListDirErrorPropagates(t *testing.T) {
	host := &hostfs.Fake{Err: errors.New("boom")}
	d := baseDeps(recycleCfg(), withHost(host))
	if _, err := (recyclePlanner{}).Plan(context.Background(), d); err == nil {
		t.Fatal("expected listdir error to propagate")
	}
}

func TestRecyclePlanDirSizeErrorPropagates(t *testing.T) {
	old := fixedNow.Add(-48 * time.Hour)
	host := &hostfs.Fake{
		Entries: map[string][]hostfs.Entry{"/recycle": {{Name: "old-dir", IsDir: true, ModTime: old}}},
	}
	// Sizes has no entry for /recycle/old-dir, but DirSize on the real
	// client would still succeed with 0; force an error via a wrapper
	// that fails only DirSize.
	failing := dirSizeFailFake{Fake: host, err: errors.New("dirsize boom")}
	d := baseDeps(recycleCfg(), withHost(failing))
	if _, err := (recyclePlanner{}).Plan(context.Background(), d); err == nil {
		t.Fatal("expected dirsize error to propagate")
	}
}

// dirSizeFailFake delegates to an embedded *hostfs.Fake for everything
// except DirSize, which always fails.
type dirSizeFailFake struct {
	*hostfs.Fake
	err error
}

func (f dirSizeFailFake) DirSize(context.Context, string, int) (int64, error) {
	return 0, f.err
}

func TestRecycleExecuteRemovesOnlyItemsUnderConfiguredDirs(t *testing.T) {
	host := &hostfs.Fake{}
	cfg := enabledCfg(func(c *config.Config) { c.Checks.RecycleDirs = []string{"/recycle"} })
	d := baseDeps(cfg, withHost(host))

	plan := Plan{
		Kind: KindRecycle,
		Items: []PlanItem{
			{Key: "/recycle/old.mkv", Bytes: 100},
			{Key: "/etc/passwd", Bytes: 5}, // outside RecycleDirs: must be refused
		},
	}
	res, err := Execute(context.Background(), d, plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Executed != 1 || res.Bytes != 100 {
		t.Fatalf("Result = %+v, want Executed=1 Bytes=100", res)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("Errors = %v, want exactly one refusal", res.Errors)
	}
	if len(host.Removed) != 1 || host.Removed[0] != "/recycle/old.mkv" {
		t.Fatalf("Removed = %v, want only /recycle/old.mkv", host.Removed)
	}
}

func TestRecycleExecuteNoHostClientErrors(t *testing.T) {
	cfg := enabledCfg(func(c *config.Config) { c.Checks.RecycleDirs = []string{"/recycle"} })
	d := baseDeps(cfg)
	if _, err := Execute(context.Background(), d, Plan{Kind: KindRecycle}); err == nil {
		t.Fatal("expected an error with no host client configured")
	}
}

func TestRecycleExecuteRecordsPerItemRemoveError(t *testing.T) {
	host := &hostfs.Fake{Err: errors.New("permission denied")}
	cfg := enabledCfg(func(c *config.Config) { c.Checks.RecycleDirs = []string{"/recycle"} })
	d := baseDeps(cfg, withHost(host))

	plan := Plan{Kind: KindRecycle, Items: []PlanItem{{Key: "/recycle/old.mkv", Bytes: 100}}}
	res, err := Execute(context.Background(), d, plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Executed != 0 || len(res.Errors) != 1 {
		t.Fatalf("Result = %+v, want Executed=0 and one error", res)
	}
}
