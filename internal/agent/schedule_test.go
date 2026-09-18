package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/notify"
	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
)

var scheduleT0 = time.Date(2026, 9, 19, 12, 3, 0, 0, time.UTC)

// scheduleTestCfg is a Config with every [agent]/[email] field Schedule
// reads set to a valid, non-default-looking value, so a test that forgets
// to set one fails loudly (an empty CheckpointAt/DigestAt is a parse
// error, not a silently-skipped entry).
func scheduleTestCfg(node config.Node) config.Config {
	return config.Config{
		Node:   node,
		Checks: config.Checks{Timeout: 5 * time.Second},
		Agent: config.Agent{
			HeartbeatInterval:    5 * time.Minute,
			CheckpointAt:         "03:00",
			PeerStaleAfter:       15 * time.Minute,
			PeerMessageRetention: 30 * 24 * time.Hour,
		},
		Email: config.Email{DigestAt: "07:00", To: "ops@example.test"},
	}
}

func newScheduleTestAgent(t *testing.T, mutate func(*Options)) *Agent {
	t.Helper()
	o := Options{
		Cfg:      scheduleTestCfg(config.NodePi),
		Registry: check.NewRegistry(),
		Deps:     fakeDeps(check.Deps{}, nil),
		Store:    &fakeStore{},
		Now:      fixedNow(scheduleT0),
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
	return a
}

// expectedSpecs returns the cron specs Schedule should register for the
// given scenario, mirroring Schedule's own conditionals (hasPeer gates the
// heartbeat entry, hasSender gates the digest entry) so assertions don't
// hardcode a separate copy of that logic.
func expectedSpecs(t *testing.T, cfg config.Config, hasPeer, hasSender bool) []string {
	t.Helper()
	specs := []string{every5mSpec, every15mSpec, hourlySpec, dailyCycleSpec}
	if hasPeer {
		specs = append(specs, fmt.Sprintf("@every %s", cfg.Agent.HeartbeatInterval))
	}
	if hasSender {
		spec, err := dailyCronSpec(cfg.Email.DigestAt)
		if err != nil {
			t.Fatalf("dailyCronSpec(digest): %v", err)
		}
		specs = append(specs, spec)
	}
	spec, err := dailyCronSpec(cfg.Agent.CheckpointAt)
	if err != nil {
		t.Fatalf("dailyCronSpec(checkpoint): %v", err)
	}
	specs = append(specs, spec)
	return specs
}

// assertEntriesMatchSpecs proves c holds exactly the given specs, compared
// as a multiset of each spec's Next(scheduleT0) (order-independent).
func assertEntriesMatchSpecs(t *testing.T, c *cron.Cron, specs []string) {
	t.Helper()
	entries := c.Entries()
	if len(entries) != len(specs) {
		t.Fatalf("len(Entries()) = %d, want %d (specs=%v)", len(entries), len(specs), specs)
	}

	want := make(map[time.Time]int, len(specs))
	for _, spec := range specs {
		sched, err := cron.ParseStandard(spec)
		if err != nil {
			t.Fatalf("ParseStandard(%q): %v", spec, err)
		}
		want[sched.Next(scheduleT0)]++
	}

	got := make(map[time.Time]int, len(entries))
	for _, e := range entries {
		got[e.Schedule.Next(scheduleT0)]++
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries' next-run times = %v, want %v (specs=%v)", got, want, specs)
	}
}

// TestScheduleEntriesMatchConfiguration proves Schedule registers exactly
// the entries this Peer/Sender/node combination calls for (the NAS, with
// a Sender left nil per constraints.md, omits the digest entry).
func TestScheduleEntriesMatchConfiguration(t *testing.T) {
	cases := []struct {
		name               string
		node               config.Node
		hasPeer, hasSender bool
	}{
		{"pi with peer and sender has every entry", config.NodePi, true, true},
		{"nas omits digest", config.NodeNAS, true, false},
		{"no peer omits heartbeat", config.NodePi, false, true},
		{"neither peer nor sender", config.NodePi, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newScheduleTestAgent(t, func(o *Options) {
				o.Cfg = scheduleTestCfg(tc.node)
				if tc.hasPeer {
					o.Peer = &peer.Fake{}
				}
				if tc.hasSender {
					o.Sender = &notify.FakeSender{}
				}
			})

			c, err := a.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() err = %v", err)
			}
			assertEntriesMatchSpecs(t, c, expectedSpecs(t, a.cfg, tc.hasPeer, tc.hasSender))
		})
	}
}

// TestScheduleReturnsNotStartedCron proves Schedule never starts the
// returned *cron.Cron itself (Run does): every entry's Next/Prev is still
// the zero value.
func TestScheduleReturnsNotStartedCron(t *testing.T) {
	a := newScheduleTestAgent(t, nil)

	c, err := a.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() err = %v", err)
	}
	for _, e := range c.Entries() {
		if !e.Next.IsZero() || !e.Prev.IsZero() {
			t.Fatalf("entry %+v has a non-zero Next/Prev before Start()", e)
		}
	}
}

func TestScheduleRejectsInvalidConfig(t *testing.T) {
	cases := map[string]func(*Options){
		"invalid timezone":      func(o *Options) { o.Cfg.Agent.Timezone = "not/a/real/zone" },
		"invalid checkpoint_at": func(o *Options) { o.Cfg.Agent.CheckpointAt = "not-a-time" },
		"invalid digest_at": func(o *Options) {
			o.Sender = &notify.FakeSender{} // digest is only scheduled with a Sender
			o.Cfg.Email.DigestAt = "not-a-time"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := newScheduleTestAgent(t, mutate)
			if _, err := a.Schedule(context.Background()); err == nil {
				t.Fatal("Schedule() err = nil, want an error")
			}
		})
	}
}

// TestScheduleCycleAndCheckpointJobsRunTheirWork drives every entry's raw
// Job (bypassing cron's own timer loop entirely) to prove the closures
// Schedule wires up actually call through to RunCycle/Checkpoint with this
// agent's dependencies, not just that the specs line up.
func TestScheduleCycleAndCheckpointJobsRunTheirWork(t *testing.T) {
	fs := &fakeStore{}
	reg := registryWith(noopCheck("pi-5m", config.NodePi, fiveMin))
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Registry = reg
		o.Store = fs
		o.Deps = fakeDeps(check.Deps{Node: config.NodePi, Now: func() time.Time { return scheduleT0 }}, nil)
	})

	c, err := a.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() err = %v", err)
	}
	runAllJobs(c)

	if len(fs.SavedReports) != 1 {
		t.Fatalf("SavedReports = %d, want 1 (only the 5m cadence matches a check)", len(fs.SavedReports))
	}
	if fs.CheckpointCalls != 1 {
		t.Fatalf("CheckpointCalls = %d, want 1", fs.CheckpointCalls)
	}
}

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
	if strings.Contains(logBuf.String(), "level=ERROR") {
		t.Fatalf("log = %q, want no ERROR-level entry for a busy (expected) checkpoint", logBuf.String())
	}
}

// TestScheduleHeartbeatJobRuns proves the heartbeat entry's job calls
// through to the peer client with this node's identity.
func TestScheduleHeartbeatJobRuns(t *testing.T) {
	fp := &peer.Fake{}
	a := newScheduleTestAgent(t, func(o *Options) { o.Peer = fp })

	c, err := a.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() err = %v", err)
	}
	runAllJobs(c)

	if want := []string{"Heartbeat(pi)"}; !reflect.DeepEqual(fp.Calls, want) {
		t.Fatalf("peer.Calls = %v, want %v", fp.Calls, want)
	}
}

// TestScheduleDigestJobRuns proves the digest entry's job sends and marks
// an email, exercising SendDigest end to end through the scheduler.
func TestScheduleDigestJobRuns(t *testing.T) {
	fs := &fakeStore{}
	fsend := &notify.FakeSender{}
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Sender = fsend
	})

	c, err := a.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() err = %v", err)
	}
	runAllJobs(c)

	if len(fsend.Sends) != 1 {
		t.Fatalf("Sends = %d, want 1", len(fsend.Sends))
	}
	if len(fs.SentEmailIDs) != 1 {
		t.Fatalf("SentEmailIDs = %d, want 1", len(fs.SentEmailIDs))
	}
}

// runAllJobs runs every entry's raw (unwrapped) Job synchronously.
func runAllJobs(c *cron.Cron) {
	for _, e := range c.Entries() {
		e.Job.Run()
	}
}

func TestDailyCronSpecFormatsHHMM(t *testing.T) {
	cases := map[string]string{
		"03:00": "0 3 * * *",
		"00:10": "10 0 * * *",
		"23:59": "59 23 * * *",
		"07:00": "0 7 * * *",
	}
	for in, want := range cases {
		got, err := dailyCronSpec(in)
		if err != nil {
			t.Fatalf("dailyCronSpec(%q) err = %v", in, err)
		}
		if got != want {
			t.Fatalf("dailyCronSpec(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDailyCronSpecRejectsMalformed(t *testing.T) {
	if _, err := dailyCronSpec("not-a-time"); err == nil {
		t.Fatal("dailyCronSpec() err = nil, want an error")
	}
}

func TestCronLoggerRoutesThroughSlog(t *testing.T) {
	var buf strings.Builder
	l := newCronLogger(slog.New(slog.NewTextHandler(&buf, nil)))

	l.Info("routine message", "k", "v")
	l.Error(errors.New("boom"), "failure message")

	out := buf.String()
	for _, want := range []string{"routine message", "failure message", "boom"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log = %q, want it to contain %q", out, want)
		}
	}
}

func TestSendHeartbeatPostsAndRecordsOutbound(t *testing.T) {
	fs := &fakeStore{}
	fp := &peer.Fake{Ack: peer.Ack{Status: "ok"}}
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Peer = fp
	})

	if err := a.SendHeartbeat(context.Background()); err != nil {
		t.Fatalf("SendHeartbeat() err = %v", err)
	}
	if want := []string{"Heartbeat(pi)"}; !reflect.DeepEqual(fp.Calls, want) {
		t.Fatalf("peer.Calls = %v, want %v", fp.Calls, want)
	}
	if len(fs.PeerMessages) != 1 {
		t.Fatalf("PeerMessages = %d, want 1", len(fs.PeerMessages))
	}
	msg := fs.PeerMessages[0]
	if msg.Direction != "out" || msg.Kind != "heartbeat" || msg.Peer != config.NodeNAS {
		t.Fatalf("PeerMessages[0] = %+v, want out/heartbeat to nas", msg)
	}
}

func TestSendHeartbeatNilPeerIsNoop(t *testing.T) {
	fs := &fakeStore{}
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Peer = nil
	})

	if err := a.SendHeartbeat(context.Background()); err != nil {
		t.Fatalf("SendHeartbeat() err = %v", err)
	}
	if len(fs.PeerMessages) != 0 {
		t.Fatalf("PeerMessages = %d, want 0 (no peer configured)", len(fs.PeerMessages))
	}
}

// TestSendHeartbeatPeerFailureLoggedNotFatal proves a peer push failure is
// logged but never fails SendHeartbeat (an unreachable peer is never fatal).
func TestSendHeartbeatPeerFailureLoggedNotFatal(t *testing.T) {
	fs := &fakeStore{}
	fp := &peer.Fake{Err: errors.New("peer down")}
	var logBuf strings.Builder
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Peer = fp
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	})

	if err := a.SendHeartbeat(context.Background()); err != nil {
		t.Fatalf("SendHeartbeat() err = %v, want nil (peer failure is never fatal)", err)
	}
	if len(fs.PeerMessages) != 1 {
		t.Fatalf("PeerMessages = %d, want 1 (recorded despite push failure)", len(fs.PeerMessages))
	}
	if !strings.Contains(logBuf.String(), "peer down") {
		t.Fatalf("log = %q, want it to mention the peer error", logBuf.String())
	}
}

func TestSendHeartbeatRecordErrorPropagates(t *testing.T) {
	wantErr := errors.New("record boom")
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{SavePeerMessageErr: wantErr}
		o.Peer = &peer.Fake{}
	})

	if err := a.SendHeartbeat(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("SendHeartbeat() err = %v, want wrapping %v", err, wantErr)
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

// TestScheduleDigestDisabledIsWarnedAndSkipped proves a Pi that cannot
// actually mail a digest says so at Warn and registers no digest entry,
// rather than scheduling a job that would fail nightly (no sender) or mail
// nobody (no email.to).
func TestScheduleDigestDisabledIsWarnedAndSkipped(t *testing.T) {
	cases := map[string]func(*Options){
		"sender but no email.to": func(o *Options) {
			o.Sender = &notify.FakeSender{}
			o.Cfg.Email.To = ""
		},
		"email.to but no sender": func(o *Options) { o.Sender = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var logBuf strings.Builder
			a := newScheduleTestAgent(t, func(o *Options) {
				mutate(o)
				o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
			})

			c, err := a.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() err = %v", err)
			}
			assertEntriesMatchSpecs(t, c, expectedSpecs(t, a.cfg, false, false))

			out := logBuf.String()
			if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "digest disabled") {
				t.Fatalf("log = %q, want a WARN naming the disabled digest", out)
			}
		})
	}
}

// TestScheduleNASWithoutSenderDoesNotWarn proves the NAS's missing digest
// is the documented arrangement (constraints.md: only the Pi mails), not
// something to warn about every start-up.
func TestScheduleNASWithoutSenderDoesNotWarn(t *testing.T) {
	var logBuf strings.Builder
	a := newScheduleTestAgent(t, func(o *Options) {
		o.Cfg = scheduleTestCfg(config.NodeNAS)
		o.Sender = nil
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	})

	if _, err := a.Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() err = %v", err)
	}
	if strings.Contains(logBuf.String(), "digest disabled") {
		t.Fatalf("log = %q, want no digest warning on the nas", logBuf.String())
	}
}
