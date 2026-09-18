package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/peer"
)

const fiveMin = 5 * time.Minute
const fifteenMin = 15 * time.Minute

var cycleT0 = time.Date(2026, 9, 19, 5, 0, 0, 0, time.UTC)

// testCfg builds a Config with a real (non-zero) check timeout: a zero
// Checks.Timeout makes check.Run's per-check context expire before the
// check goroutine even starts, which starves every check instead of
// running it.
func testCfg(node config.Node) config.Config {
	return config.Config{Node: node, Checks: config.Checks{Timeout: 5 * time.Second}}
}

func newTestAgent(t *testing.T, mutate func(*Options)) (*Agent, *Options) {
	t.Helper()
	o := Options{
		Cfg:      testCfg(config.NodePi),
		Registry: check.NewRegistry(),
		Deps:     fakeDeps(check.Deps{}, nil),
		Store:    &fakeStore{},
		Now:      fixedNow(cycleT0),
		Version:  "v-test",
		Logger:   slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	}
	if mutate != nil {
		mutate(&o)
	}
	a, err := New(o)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	return a, &o
}

// TestRunCycleFiltersByCadenceAndNode proves the 5m cycle on pi runs only
// the 5m pi check, skipping a 15m pi check and a 5m nas-only check.
func TestRunCycleFiltersByCadenceAndNode(t *testing.T) {
	fs := &fakeStore{}
	reg := registryWith(
		noopCheck("pi-5m", config.NodePi, fiveMin),
		noopCheck("pi-15m", config.NodePi, fifteenMin),
		noopCheck("nas-5m", config.NodeNAS, fiveMin),
	)
	a, _ := newTestAgent(t, func(o *Options) {
		o.Registry = reg
		o.Store = fs
		o.Deps = fakeDeps(check.Deps{Node: config.NodePi, Now: func() time.Time { return cycleT0 }}, nil)
	})

	rep, id, err := a.RunCycle(context.Background(), fiveMin)
	if err != nil {
		t.Fatalf("RunCycle() err = %v", err)
	}
	if id != 1 {
		t.Fatalf("id = %d, want 1", id)
	}
	if got := rep.Ran; len(got) != 1 || got[0] != "pi-5m" {
		t.Fatalf("rep.Ran = %v, want [pi-5m]", got)
	}
	if len(fs.SavedReports) != 1 {
		t.Fatalf("SavedReports = %d, want 1", len(fs.SavedReports))
	}
}

// TestRunCycleUnknownCadenceSkipsEverything covers the "no checks match"
// branch: an empty report, id 0, nil error, and no store/deps interaction.
func TestRunCycleUnknownCadenceSkipsEverything(t *testing.T) {
	fs := &fakeStore{}
	depsCalled := false
	reg := registryWith(noopCheck("pi-5m", config.NodePi, fiveMin))
	a, _ := newTestAgent(t, func(o *Options) {
		o.Registry = reg
		o.Store = fs
		o.Deps = func(context.Context) (check.Deps, error) {
			depsCalled = true
			return check.Deps{}, nil
		}
	})

	rep, id, err := a.RunCycle(context.Background(), fifteenMin)
	if err != nil {
		t.Fatalf("RunCycle() err = %v", err)
	}
	if id != 0 {
		t.Fatalf("id = %d, want 0", id)
	}
	if rep.Node != config.NodePi || len(rep.Findings) != 0 || rep.Findings == nil {
		t.Fatalf("rep = %+v, want empty report for pi", rep)
	}
	if !rep.GeneratedAt.Equal(cycleT0) {
		t.Fatalf("rep.GeneratedAt = %v, want %v", rep.GeneratedAt, cycleT0)
	}
	if depsCalled {
		t.Fatal("Deps was called for an unknown cadence")
	}
	if len(fs.SavedReports) != 0 {
		t.Fatalf("SavedReports = %d, want 0 (nothing should be saved)", len(fs.SavedReports))
	}
}

// TestRunCyclePassesPreviousThrough proves Deps.Previous is set from the
// store's latest report before the checks run.
func TestRunCyclePassesPreviousThrough(t *testing.T) {
	prev := check.Report{Node: config.NodePi, Metrics: map[string]float64{"sonarr_wanted_missing": 3}}
	fs := &fakeStore{LatestReportFound: true, LatestReportResult: prev}
	var got check.Deps
	reg := registryWith(recordingCheck("pi-5m", config.NodePi, fiveMin, &got))
	a, _ := newTestAgent(t, func(o *Options) {
		o.Registry = reg
		o.Store = fs
		o.Deps = fakeDeps(check.Deps{Node: config.NodePi, Now: func() time.Time { return cycleT0 }}, nil)
	})

	if _, _, err := a.RunCycle(context.Background(), fiveMin); err != nil {
		t.Fatalf("RunCycle() err = %v", err)
	}
	if got.Previous == nil {
		t.Fatal("Deps.Previous = nil, want the stored report")
	}
	if !reflect.DeepEqual(*got.Previous, prev) {
		t.Fatalf("Deps.Previous = %+v, want %+v", *got.Previous, prev)
	}
}

// TestRunCycleNASPushesReport proves a NAS cycle with a peer configured
// pushes the saved report and records an "out"/"report" peer message.
func TestRunCycleNASPushesReport(t *testing.T) {
	fs := &fakeStore{}
	fp := &peer.Fake{Ack: peer.Ack{Status: "ok"}}
	reg := registryWith(noopCheck("nas-5m", config.NodeNAS, fiveMin))
	a, _ := newTestAgent(t, func(o *Options) {
		o.Cfg = testCfg(config.NodeNAS)
		o.Registry = reg
		o.Store = fs
		o.Peer = fp
		o.Deps = fakeDeps(check.Deps{Node: config.NodeNAS, Now: func() time.Time { return cycleT0 }}, nil)
	})

	rep, id, err := a.RunCycle(context.Background(), fiveMin)
	if err != nil {
		t.Fatalf("RunCycle() err = %v", err)
	}
	if want := []string{"PushReport(nas)"}; !reflect.DeepEqual(fp.Calls, want) {
		t.Fatalf("peer.Calls = %v, want %v", fp.Calls, want)
	}
	if len(fs.PeerMessages) != 1 {
		t.Fatalf("PeerMessages = %d, want 1", len(fs.PeerMessages))
	}
	msg := fs.PeerMessages[0]
	if msg.Direction != "out" || msg.Kind != "report" || msg.Peer != config.NodePi {
		t.Fatalf("PeerMessages[0] = %+v, want out/report to pi", msg)
	}
	var env peer.ReportEnvelope
	if err := json.Unmarshal(msg.Payload, &env); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if env.Node != config.NodeNAS || !reflect.DeepEqual(env.Report, rep) {
		t.Fatalf("envelope payload = %+v, want node=nas report=%+v", env, rep)
	}
	if id != 1 {
		t.Fatalf("id = %d, want 1", id)
	}
}

// TestRunCyclePushFailureLoggedAndRecorded proves a push failure never
// fails the cycle: it's logged and still recorded as an outbound message.
func TestRunCyclePushFailureLoggedAndRecorded(t *testing.T) {
	fs := &fakeStore{}
	pushErr := errors.New("peer unreachable")
	fp := &peer.Fake{Err: pushErr}
	var logBuf strings.Builder
	reg := registryWith(noopCheck("nas-5m", config.NodeNAS, fiveMin))
	a, _ := newTestAgent(t, func(o *Options) {
		o.Cfg = testCfg(config.NodeNAS)
		o.Registry = reg
		o.Store = fs
		o.Peer = fp
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
		o.Deps = fakeDeps(check.Deps{Node: config.NodeNAS, Now: func() time.Time { return cycleT0 }}, nil)
	})

	_, _, err := a.RunCycle(context.Background(), fiveMin)
	if err != nil {
		t.Fatalf("RunCycle() err = %v, want nil (push failures are never fatal)", err)
	}
	if len(fs.PeerMessages) != 1 {
		t.Fatalf("PeerMessages = %d, want 1 (recorded despite push failure)", len(fs.PeerMessages))
	}
	if !strings.Contains(logBuf.String(), "peer unreachable") {
		t.Fatalf("log = %q, want it to mention the push error", logBuf.String())
	}
}

// TestRunCycleDepsErrorPropagates proves a Deps build failure is returned
// (not swallowed), unlike a push failure.
func TestRunCycleDepsErrorPropagates(t *testing.T) {
	wantErr := errors.New("build deps boom")
	reg := registryWith(noopCheck("pi-5m", config.NodePi, fiveMin))
	a, _ := newTestAgent(t, func(o *Options) {
		o.Registry = reg
		o.Deps = fakeDeps(check.Deps{}, wantErr)
	})

	if _, _, err := a.RunCycle(context.Background(), fiveMin); !errors.Is(err, wantErr) {
		t.Fatalf("RunCycle() err = %v, want wrapping %v", err, wantErr)
	}
}

// TestRunCycleLatestReportErrorPropagates proves a store.LatestReport
// failure is returned.
func TestRunCycleLatestReportErrorPropagates(t *testing.T) {
	wantErr := errors.New("latest report boom")
	reg := registryWith(noopCheck("pi-5m", config.NodePi, fiveMin))
	a, _ := newTestAgent(t, func(o *Options) {
		o.Registry = reg
		o.Store = &fakeStore{LatestReportErr: wantErr}
		o.Deps = fakeDeps(check.Deps{Node: config.NodePi, Now: func() time.Time { return cycleT0 }}, nil)
	})

	if _, _, err := a.RunCycle(context.Background(), fiveMin); !errors.Is(err, wantErr) {
		t.Fatalf("RunCycle() err = %v, want wrapping %v", err, wantErr)
	}
}

// TestRunCycleSaveReportErrorPropagates proves a store.SaveReport
// failure is returned.
func TestRunCycleSaveReportErrorPropagates(t *testing.T) {
	wantErr := errors.New("save report boom")
	reg := registryWith(noopCheck("pi-5m", config.NodePi, fiveMin))
	a, _ := newTestAgent(t, func(o *Options) {
		o.Registry = reg
		o.Store = &fakeStore{SaveReportErr: wantErr}
		o.Deps = fakeDeps(check.Deps{Node: config.NodePi, Now: func() time.Time { return cycleT0 }}, nil)
	})

	if _, _, err := a.RunCycle(context.Background(), fiveMin); !errors.Is(err, wantErr) {
		t.Fatalf("RunCycle() err = %v, want wrapping %v", err, wantErr)
	}
}

// TestRunCyclePiNeverPushes proves the Pi node never pushes even with a
// peer configured (only the NAS pushes reports, per the brief).
func TestRunCyclePiNeverPushes(t *testing.T) {
	fs := &fakeStore{}
	fp := &peer.Fake{}
	reg := registryWith(noopCheck("pi-5m", config.NodePi, fiveMin))
	a, _ := newTestAgent(t, func(o *Options) {
		o.Registry = reg
		o.Store = fs
		o.Peer = fp
		o.Deps = fakeDeps(check.Deps{Node: config.NodePi, Now: func() time.Time { return cycleT0 }}, nil)
	})

	if _, _, err := a.RunCycle(context.Background(), fiveMin); err != nil {
		t.Fatalf("RunCycle() err = %v", err)
	}
	if len(fp.Calls) != 0 {
		t.Fatalf("peer.Calls = %v, want none (pi never pushes)", fp.Calls)
	}
	if len(fs.PeerMessages) != 0 {
		t.Fatalf("PeerMessages = %d, want 0", len(fs.PeerMessages))
	}
}

// TestRunCycleNoPeerNeverPushes proves a nil Peer skips the push branch
// entirely even on the NAS.
func TestRunCycleNoPeerNeverPushes(t *testing.T) {
	fs := &fakeStore{}
	reg := registryWith(noopCheck("nas-5m", config.NodeNAS, fiveMin))
	a, _ := newTestAgent(t, func(o *Options) {
		o.Cfg = testCfg(config.NodeNAS)
		o.Registry = reg
		o.Store = fs
		o.Peer = nil
		o.Deps = fakeDeps(check.Deps{Node: config.NodeNAS, Now: func() time.Time { return cycleT0 }}, nil)
	})

	if _, _, err := a.RunCycle(context.Background(), fiveMin); err != nil {
		t.Fatalf("RunCycle() err = %v", err)
	}
	if len(fs.PeerMessages) != 0 {
		t.Fatalf("PeerMessages = %d, want 0 (no peer configured)", len(fs.PeerMessages))
	}
}
