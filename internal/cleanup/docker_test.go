package cleanup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/docker"
	"github.com/parsoFish/healarr/internal/config"
)

func TestDockerPlanNotConfigured(t *testing.T) {
	d := baseDeps(config.Config{})
	if _, err := (dockerPlanner{}).Plan(context.Background(), d); !errors.Is(err, check.ErrNotConfigured) {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
}

func TestDockerPlanNothingReclaimableIsEmpty(t *testing.T) {
	dk := &docker.Fake{Usage: docker.DiskUsage{ImagesReclaimable: 0}}
	d := baseDeps(config.Config{Cleanup: config.Cleanup{DockerDangling: true}}, withDocker(dk))
	plan, err := (dockerPlanner{}).Plan(context.Background(), d)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Items) != 0 || plan.Bytes != 0 {
		t.Fatalf("plan = %+v, want empty", plan)
	}
}

func TestDockerPlanReclaimableProducesOneAggregateItem(t *testing.T) {
	dk := &docker.Fake{Usage: docker.DiskUsage{ImagesReclaimable: 3_000_000_000}}
	d := baseDeps(config.Config{Cleanup: config.Cleanup{DockerDangling: true}}, withDocker(dk))
	plan, err := (dockerPlanner{}).Plan(context.Background(), d)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Items) != 1 || plan.Bytes != 3_000_000_000 {
		t.Fatalf("plan = %+v, want one item totalling 3e9 bytes", plan)
	}
	if !strings.Contains(plan.Items[0].Detail, "dangling") {
		t.Errorf("Detail = %q, want it to mention dangling", plan.Items[0].Detail)
	}
}

func TestDockerPlanDescribesUnusedWhenDanglingOnlyIsOff(t *testing.T) {
	dk := &docker.Fake{Usage: docker.DiskUsage{ImagesReclaimable: 1}}
	d := baseDeps(config.Config{Cleanup: config.Cleanup{DockerDangling: false}}, withDocker(dk))
	plan, err := (dockerPlanner{}).Plan(context.Background(), d)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !strings.Contains(plan.Items[0].Detail, "unused") {
		t.Errorf("Detail = %q, want it to mention unused", plan.Items[0].Detail)
	}
}

func TestDockerPlanDiskUsageErrorPropagates(t *testing.T) {
	dk := &docker.Fake{Err: errors.New("boom")}
	d := baseDeps(config.Config{}, withDocker(dk))
	if _, err := (dockerPlanner{}).Plan(context.Background(), d); err == nil {
		t.Fatal("expected DiskUsage error to propagate")
	}
}

func TestDockerExecuteNoClientErrors(t *testing.T) {
	d := baseDeps(enabledCfg(nil))
	if _, err := Execute(context.Background(), d, Plan{Kind: KindDocker}); err == nil {
		t.Fatal("expected an error with no docker client configured")
	}
}

func TestDockerExecutePrunesAndReportsActualReclaimed(t *testing.T) {
	dk := &docker.Fake{Usage: docker.DiskUsage{ImagesReclaimable: 42}}
	cfg := enabledCfg(func(c *config.Config) { c.Cleanup.DockerDangling = true })
	d := baseDeps(cfg, withDocker(dk))

	plan := Plan{Kind: KindDocker, Items: []PlanItem{{Key: dockerImagesKey, Bytes: 100}}}
	res, err := Execute(context.Background(), d, plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Fake.PruneImages always returns Usage.ImagesReclaimable, proving
	// Result.Bytes comes from the prune call's own return value (42) and
	// not the plan's earlier estimate (100).
	if res.Executed != 1 || res.Bytes != 42 {
		t.Fatalf("Result = %+v, want Executed=1 Bytes=42", res)
	}
	if len(dk.Calls) != 1 || dk.Calls[0] != "PruneImages(true)" {
		t.Fatalf("Calls = %v, want PruneImages(true)", dk.Calls)
	}
}

func TestDockerExecutePruneErrorPropagates(t *testing.T) {
	dk := &docker.Fake{Err: errors.New("prune boom")}
	d := baseDeps(enabledCfg(nil), withDocker(dk))
	if _, err := Execute(context.Background(), d, Plan{Kind: KindDocker}); err == nil {
		t.Fatal("expected PruneImages error to propagate")
	}
}
