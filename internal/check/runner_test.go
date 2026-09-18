package check

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

var fixedNow = time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)

func testDeps() Deps { return Deps{Node: config.NodePi, Now: func() time.Time { return fixedNow }} }

func TestRunCollectsFindingsErrorsAndSkips(t *testing.T) {
	checks := []Check{
		{ID: "ok", Nodes: []config.Node{config.NodePi}, Run: func(_ context.Context, d Deps) (Result, error) {
			return Result{Findings: []Finding{d.NewFinding("ok", "e1", SeverityWarn, TierObserve, "warn one")}, Metrics: map[string]float64{"m": 3}}, nil
		}},
		{ID: "crit", Nodes: []config.Node{config.NodePi}, Run: func(_ context.Context, d Deps) (Result, error) {
			return Result{Findings: []Finding{d.NewFinding("crit", "e2", SeverityCritical, TierCorrect, "crit")}}, nil
		}},
		{ID: "bad", Nodes: []config.Node{config.NodePi}, Run: func(context.Context, Deps) (Result, error) { return Result{}, errors.New("boom") }},
		{ID: "skip", Nodes: []config.Node{config.NodePi}, Run: func(context.Context, Deps) (Result, error) { return Result{}, ErrNotConfigured }},
	}
	rep := Run(context.Background(), checks, testDeps(), time.Second)
	if rep.ChecksRun != 3 || rep.ChecksFailed != 1 {
		t.Fatalf("run/failed = %d/%d", rep.ChecksRun, rep.ChecksFailed)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0] != "skip" {
		t.Fatalf("skipped: %v", rep.Skipped)
	}
	if len(rep.Errors) != 1 || rep.Errors[0].CheckID != "bad" || rep.Errors[0].Error != "check bad: boom" {
		t.Fatalf("errors: %+v", rep.Errors)
	}
	if len(rep.Findings) != 2 || rep.Findings[0].Severity != SeverityCritical {
		t.Fatalf("findings not sorted critical-first: %+v", rep.Findings)
	}
	if rep.Metrics["m"] != 3 {
		t.Fatalf("metrics: %v", rep.Metrics)
	}
	if !rep.GeneratedAt.Equal(fixedNow) || rep.Findings[0].FirstSeen != fixedNow {
		t.Fatal("timestamps not from Deps.Now")
	}
}

func TestRunTimesOutSlowCheck(t *testing.T) {
	slow := Check{ID: "slow", Nodes: []config.Node{config.NodePi}, Run: func(ctx context.Context, _ Deps) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	}}
	rep := Run(context.Background(), []Check{slow}, testDeps(), 10*time.Millisecond)
	if rep.ChecksFailed != 1 || !strings.Contains(rep.Errors[0].Error, "deadline") {
		t.Fatalf("expected deadline error, got %+v", rep.Errors)
	}
}

func TestRunRecoversPanic(t *testing.T) {
	p := Check{ID: "p", Nodes: []config.Node{config.NodePi}, Run: func(context.Context, Deps) (Result, error) { panic("oops") }}
	rep := Run(context.Background(), []Check{p}, testDeps(), time.Second)
	if rep.ChecksFailed != 1 || !strings.Contains(rep.Errors[0].Error, "panicked") {
		t.Fatalf("got %+v", rep.Errors)
	}
}

func TestPreviousMetric(t *testing.T) {
	d := testDeps()
	if _, ok := d.PreviousMetric("x"); ok {
		t.Fatal("nil previous should miss")
	}
	d.Previous = &Report{Metrics: map[string]float64{"x": 1}}
	if v, ok := d.PreviousMetric("x"); !ok || v != 1 {
		t.Fatal("previous metric lookup")
	}
}
