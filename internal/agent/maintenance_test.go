package agent

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/store"
)

// TestScheduleCheckpointJobLogsBusyAsWarn proves a store.ErrCheckpointBusy
// from the scheduled checkpoint job is logged (not silently dropped) but
// at Warn, not Error, since it's expected to clear up on its own.
func TestScheduleCheckpointJobLogsBusyAsWarn(t *testing.T) {
	fs := &fakeStore{CheckpointErr: store.ErrCheckpointBusy}
	var logBuf strings.Builder
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	})

	c, err := a.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() err = %v", err)
	}
	runAllJobs(c)

	if fs.CheckpointCalls != 1 {
		t.Fatalf("CheckpointCalls = %d, want 1", fs.CheckpointCalls)
	}
	if !strings.Contains(logBuf.String(), "level=WARN") {
		t.Fatalf("log = %q, want a WARN-level entry for the busy checkpoint", logBuf.String())
	}
	// Scoped to lines naming the checkpoint itself: runAllJobs also fires
	// the daily cadence's chained cleanup-planning job (cleanupjob.go),
	// which logs its own ERROR line on this test's zero-value Deps (no
	// Docker client configured) — expected noise from an unrelated job,
	// not a checkpoint failure.
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, "checkpoint") && strings.Contains(line, "level=ERROR") {
			t.Fatalf("log line = %q, want no ERROR-level entry for a busy (expected) checkpoint", line)
		}
	}
}

// TestScheduleCheckpointJobPrunesBeforeCheckpoint proves the nightly job
// trims peer_messages to Agent.PeerMessageRetention *before* it
// checkpoints, so the WAL truncation runs after the deletes rather than
// leaving a night's worth of them in the WAL.
func TestScheduleCheckpointJobPrunesBeforeCheckpoint(t *testing.T) {
	const retention = 48 * time.Hour
	fs := &fakeStore{PrunePeerMessagesDeleted: 7}
	var logBuf strings.Builder
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Cfg.Agent.PeerMessageRetention = retention
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	})

	c, err := a.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() err = %v", err)
	}
	runAllJobs(c)

	if want := []string{"prune", "checkpoint"}; !reflect.DeepEqual(fs.Ops, want) {
		t.Fatalf("store ops = %v, want %v", fs.Ops, want)
	}
	if len(fs.PrunedBefore) != 1 {
		t.Fatalf("PrunedBefore = %v, want exactly one cutoff", fs.PrunedBefore)
	}
	if want := scheduleT0.Add(-retention); !fs.PrunedBefore[0].Equal(want) {
		t.Fatalf("prune cutoff = %v, want %v (now - peer_message_retention)", fs.PrunedBefore[0], want)
	}
	if out := logBuf.String(); !strings.Contains(out, "pruned_peer_messages=7") {
		t.Fatalf("checkpoint log = %q, want it to report the pruned row count", out)
	}
}

// TestScheduleCheckpointJobLogsPruneFailureAndStillCheckpoints proves a
// failed prune is logged rather than swallowed, and never costs the night
// its checkpoint — the two are independent pieces of housekeeping.
func TestScheduleCheckpointJobLogsPruneFailureAndStillCheckpoints(t *testing.T) {
	fs := &fakeStore{PrunePeerMessagesErr: errors.New("prune boom")}
	var logBuf strings.Builder
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	})

	c, err := a.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() err = %v", err)
	}
	runAllJobs(c)

	if fs.CheckpointCalls != 1 {
		t.Fatalf("CheckpointCalls = %d, want 1 (a failed prune must not skip the checkpoint)", fs.CheckpointCalls)
	}
	if !strings.Contains(logBuf.String(), "prune boom") {
		t.Fatalf("log = %q, want it to mention the prune error", logBuf.String())
	}
}
