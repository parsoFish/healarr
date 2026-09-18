package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/qbittorrent"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/peer"
)

var handlerT0 = time.Date(2026, 9, 19, 6, 0, 0, 0, time.UTC)

func newHandlerTestAgent(t *testing.T, mutate func(*Options)) (*Agent, *fakeStore, *peer.Fake) {
	t.Helper()
	fs := &fakeStore{}
	fp := &peer.Fake{}
	o := Options{
		Cfg:      config.Config{Node: config.NodePi},
		Registry: check.NewRegistry(),
		Deps:     fakeDeps(check.Deps{}, nil),
		Store:    fs,
		Peer:     fp,
		Now:      fixedNow(handlerT0),
		Version:  "v-test",
	}
	if mutate != nil {
		mutate(&o)
	}
	a, err := New(o)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	return a, fs, fp
}

func TestPeerHandlerReturnsUsableAdapter(t *testing.T) {
	a, fs, _ := newHandlerTestAgent(t, nil)
	h := a.PeerHandler()
	hb := peer.Heartbeat{Node: config.NodeNAS, At: handlerT0, Version: "v", UptimeSeconds: 5}
	if err := h.ReceiveHeartbeat(context.Background(), hb); err != nil {
		t.Fatalf("ReceiveHeartbeat() err = %v", err)
	}
	if len(fs.PeerMessages) != 1 {
		t.Fatalf("PeerMessages = %d, want 1", len(fs.PeerMessages))
	}
}

func TestReceiveReportStoresWithEnvelopeNodeAndRecords(t *testing.T) {
	a, fs, _ := newHandlerTestAgent(t, nil)
	env := peer.ReportEnvelope{
		Node:    config.NodeNAS,
		SentAt:  handlerT0,
		Version: "peer-v",
		Report:  check.Report{Node: "", GeneratedAt: handlerT0, Findings: []check.Finding{}},
	}

	id, err := a.ReceiveReport(context.Background(), env)
	if err != nil {
		t.Fatalf("ReceiveReport() err = %v", err)
	}
	if id != 1 {
		t.Fatalf("id = %d, want 1", id)
	}
	if len(fs.SavedReports) != 1 || fs.SavedReports[0].Node != config.NodeNAS {
		t.Fatalf("SavedReports = %+v, want one report with node=nas", fs.SavedReports)
	}
	if len(fs.PeerMessages) != 1 {
		t.Fatalf("PeerMessages = %d, want 1", len(fs.PeerMessages))
	}
	msg := fs.PeerMessages[0]
	if msg.Direction != "in" || msg.Kind != "report" || msg.Peer != config.NodeNAS {
		t.Fatalf("PeerMessages[0] = %+v, want in/report from nas", msg)
	}
	var gotEnv peer.ReportEnvelope
	if err := json.Unmarshal(msg.Payload, &gotEnv); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if gotEnv.Node != config.NodeNAS || gotEnv.Version != "peer-v" {
		t.Fatalf("payload = %+v, want the received envelope", gotEnv)
	}
}

func TestReceiveReportRejectsOwnNode(t *testing.T) {
	a, fs, _ := newHandlerTestAgent(t, nil)
	env := peer.ReportEnvelope{Node: config.NodePi, SentAt: handlerT0, Report: check.Report{Node: config.NodePi}}

	_, err := a.ReceiveReport(context.Background(), env)
	if !errors.Is(err, ErrOwnNodeReport) {
		t.Fatalf("ReceiveReport() err = %v, want wrapping ErrOwnNodeReport", err)
	}
	if len(fs.SavedReports) != 0 {
		t.Fatalf("SavedReports = %d, want 0 (rejected before saving)", len(fs.SavedReports))
	}
	if len(fs.PeerMessages) != 0 {
		t.Fatalf("PeerMessages = %d, want 0 (rejected before recording)", len(fs.PeerMessages))
	}
}

func TestReceiveReportSaveErrorPropagates(t *testing.T) {
	wantErr := errors.New("save boom")
	a, _, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{SaveReportErr: wantErr}
	})
	env := peer.ReportEnvelope{Node: config.NodeNAS, Report: check.Report{Node: config.NodeNAS}}

	if _, err := a.ReceiveReport(context.Background(), env); !errors.Is(err, wantErr) {
		t.Fatalf("ReceiveReport() err = %v, want wrapping %v", err, wantErr)
	}
}

func TestReceiveReportRecordErrorPropagates(t *testing.T) {
	wantErr := errors.New("record boom")
	a, _, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{SavePeerMessageErr: wantErr}
	})
	env := peer.ReportEnvelope{Node: config.NodeNAS, Report: check.Report{Node: config.NodeNAS}}

	if _, err := a.ReceiveReport(context.Background(), env); !errors.Is(err, wantErr) {
		t.Fatalf("ReceiveReport() err = %v, want wrapping %v", err, wantErr)
	}
}

func TestLatestOwnReportFound(t *testing.T) {
	rep := check.Report{Node: config.NodePi, GeneratedAt: handlerT0, Metrics: map[string]float64{"x": 1}}
	a, _, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{LatestReportFound: true, LatestReportResult: rep}
	})

	env, ok, err := a.LatestOwnReport(context.Background())
	if err != nil || !ok {
		t.Fatalf("LatestOwnReport() ok=%v err=%v", ok, err)
	}
	if env.Node != config.NodePi || !reflect.DeepEqual(env.Report, rep) || env.Version != "v-test" {
		t.Fatalf("env = %+v, want node=pi report=%+v version=v-test", env, rep)
	}
	if !env.SentAt.Equal(handlerT0) {
		t.Fatalf("env.SentAt = %v, want %v", env.SentAt, handlerT0)
	}
}

func TestLatestOwnReportNotFound(t *testing.T) {
	a, _, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{LatestReportFound: false}
	})

	env, ok, err := a.LatestOwnReport(context.Background())
	if err != nil || ok {
		t.Fatalf("LatestOwnReport() ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if !reflect.DeepEqual(env, peer.ReportEnvelope{}) {
		t.Fatalf("env = %+v, want zero value", env)
	}
}

func TestLatestOwnReportStoreErrorPropagates(t *testing.T) {
	wantErr := errors.New("latest boom")
	a, _, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{LatestReportErr: wantErr}
	})

	if _, _, err := a.LatestOwnReport(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("LatestOwnReport() err = %v, want wrapping %v", err, wantErr)
	}
}

// TestReceiveDecisionRecordsWithoutExecuting proves a decision is only
// recorded (ADR-006 observe-only): no peer.Client call is made.
func TestReceiveDecisionRecordsWithoutExecuting(t *testing.T) {
	a, fs, fp := newHandlerTestAgent(t, nil)
	d := peer.Decision{ID: 7, Kind: "nudge", EntityKey: "sonarr:1", RequestedAt: handlerT0}

	id, err := a.ReceiveDecision(context.Background(), d)
	if err != nil {
		t.Fatalf("ReceiveDecision() err = %v", err)
	}
	if id != 1 {
		t.Fatalf("id = %d, want 1", id)
	}
	if len(fp.Calls) != 0 {
		t.Fatalf("peer.Calls = %v, want none (observe-only)", fp.Calls)
	}
	if len(fs.PeerMessages) != 1 {
		t.Fatalf("PeerMessages = %d, want 1", len(fs.PeerMessages))
	}
	msg := fs.PeerMessages[0]
	if msg.Direction != "in" || msg.Kind != "decision" || msg.Peer != config.NodeNAS {
		t.Fatalf("PeerMessages[0] = %+v, want in/decision from nas", msg)
	}
	var gotDecision peer.Decision
	if err := json.Unmarshal(msg.Payload, &gotDecision); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if !reflect.DeepEqual(gotDecision, d) {
		t.Fatalf("payload = %+v, want %+v", gotDecision, d)
	}
	if len(fs.Remediations) != 0 {
		t.Fatalf("Remediations = %+v, want none for a non-qbit_delete kind", fs.Remediations)
	}
}

func TestReceiveDecisionRecordErrorPropagates(t *testing.T) {
	wantErr := errors.New("decision boom")
	a, _, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{SavePeerMessageErr: wantErr}
	})

	if _, err := a.ReceiveDecision(context.Background(), peer.Decision{}); !errors.Is(err, wantErr) {
		t.Fatalf("ReceiveDecision() err = %v, want wrapping %v", err, wantErr)
	}
}

func TestReceiveHeartbeatRecords(t *testing.T) {
	a, fs, _ := newHandlerTestAgent(t, nil)
	hb := peer.Heartbeat{Node: config.NodeNAS, At: handlerT0, Version: "peer-v", UptimeSeconds: 42}

	if err := a.ReceiveHeartbeat(context.Background(), hb); err != nil {
		t.Fatalf("ReceiveHeartbeat() err = %v", err)
	}
	if len(fs.PeerMessages) != 1 {
		t.Fatalf("PeerMessages = %d, want 1", len(fs.PeerMessages))
	}
	msg := fs.PeerMessages[0]
	if msg.Direction != "in" || msg.Kind != "heartbeat" || msg.Peer != config.NodeNAS {
		t.Fatalf("PeerMessages[0] = %+v, want in/heartbeat from nas", msg)
	}
}

func TestReceiveHeartbeatRecordErrorPropagates(t *testing.T) {
	wantErr := errors.New("heartbeat boom")
	a, _, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{SavePeerMessageErr: wantErr}
	})

	err := a.ReceiveHeartbeat(context.Background(), peer.Heartbeat{Node: config.NodeNAS})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReceiveHeartbeat() err = %v, want wrapping %v", err, wantErr)
	}
}

// TestReceiveDecisionQbitDeleteBlockedNeverBuildsDeps proves the actions
// gate is checked before any client write (constraints.md): with actions
// disabled (the default), a "qbit_delete" decision never even builds
// check.Deps, let alone touches qBittorrent, and is recorded blocked.
func TestReceiveDecisionQbitDeleteBlockedNeverBuildsDeps(t *testing.T) {
	depsCalls := 0
	a, fs, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Deps = func(context.Context) (check.Deps, error) {
			depsCalls++
			return check.Deps{}, nil
		}
	})
	d := peer.Decision{Kind: "qbit_delete", EntityKey: "sonarr:1", Payload: map[string]any{"hashes": []string{"ABC"}}}

	if _, err := a.ReceiveDecision(context.Background(), d); err != nil {
		t.Fatalf("ReceiveDecision() err = %v, want nil (blocked, not an error)", err)
	}
	if depsCalls != 0 {
		t.Errorf("deps built %d time(s), want 0", depsCalls)
	}
	if len(fs.Remediations) != 1 {
		t.Fatalf("Remediations = %+v, want one row", fs.Remediations)
	}
	rem := fs.Remediations[0]
	if rem.Action != "qbit_delete" || rem.Status != "blocked" || rem.Node != config.NodePi {
		t.Errorf("remediation = %+v, want action=qbit_delete status=blocked node=pi", rem)
	}
}

// TestReceiveDecisionQbitDeleteExecutesWhenActionsEnabled proves that
// with actions enabled, the named hashes are actually deleted and the
// attempt is recorded executed.
func TestReceiveDecisionQbitDeleteExecutesWhenActionsEnabled(t *testing.T) {
	qb := &qbittorrent.Fake{}
	a, fs, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Cfg = config.Config{Node: config.NodePi, Actions: config.Actions{Enabled: true}}
		o.Deps = fakeDeps(check.Deps{QBit: qb}, nil)
	})
	d := peer.Decision{Kind: "qbit_delete", EntityKey: "sonarr:1", Payload: map[string]any{"hashes": []string{"abc", "def"}}}

	if _, err := a.ReceiveDecision(context.Background(), d); err != nil {
		t.Fatalf("ReceiveDecision() err = %v", err)
	}
	if len(qb.Calls) != 1 || qb.Calls[0] != "Delete([abc def], true)" {
		t.Fatalf("qbittorrent Calls = %v, want one Delete([abc def], true)", qb.Calls)
	}
	if len(fs.Remediations) != 1 || fs.Remediations[0].Status != "executed" {
		t.Fatalf("Remediations = %+v", fs.Remediations)
	}
}

// TestReceiveDecisionQbitDeleteHandlesJSONDecodedHashes proves
// hashesFromPayload also reads the []any shape a real JSON round-trip
// through map[string]any produces, not just an in-process []string.
func TestReceiveDecisionQbitDeleteHandlesJSONDecodedHashes(t *testing.T) {
	qb := &qbittorrent.Fake{}
	a, _, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Cfg = config.Config{Node: config.NodePi, Actions: config.Actions{Enabled: true}}
		o.Deps = fakeDeps(check.Deps{QBit: qb}, nil)
	})

	raw := []byte(`{"kind":"qbit_delete","entityKey":"sonarr:1","payload":{"hashes":["abc"]}}`)
	var d peer.Decision
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, err := a.ReceiveDecision(context.Background(), d); err != nil {
		t.Fatalf("ReceiveDecision() err = %v", err)
	}
	if len(qb.Calls) != 1 || qb.Calls[0] != "Delete([abc], true)" {
		t.Fatalf("qbittorrent Calls = %v, want one Delete([abc], true)", qb.Calls)
	}
}

// TestReceiveDecisionQbitDeleteClientErrorRecordsFailedWithoutPropagating
// proves a delivery failure on this node's own best-effort follow-through
// never turns into an error ReceiveDecision returns (which would make
// the Pi retry redelivering the same decision) — it is only recorded.
func TestReceiveDecisionQbitDeleteClientErrorRecordsFailedWithoutPropagating(t *testing.T) {
	qb := &qbittorrent.Fake{Err: errors.New("qbit boom")}
	a, fs, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Cfg = config.Config{Node: config.NodePi, Actions: config.Actions{Enabled: true}}
		o.Deps = fakeDeps(check.Deps{QBit: qb}, nil)
	})
	d := peer.Decision{Kind: "qbit_delete", EntityKey: "sonarr:1", Payload: map[string]any{"hashes": []string{"abc"}}}

	if _, err := a.ReceiveDecision(context.Background(), d); err != nil {
		t.Fatalf("ReceiveDecision() err = %v, want nil", err)
	}
	if len(fs.Remediations) != 1 || fs.Remediations[0].Status != "failed" || !strings.Contains(fs.Remediations[0].Detail, "qbit boom") {
		t.Fatalf("Remediations = %+v, want a failed row mentioning qbit boom", fs.Remediations)
	}
}

func TestReceiveDecisionQbitDeleteNoHashesRecordsFailed(t *testing.T) {
	a, fs, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Cfg = config.Config{Node: config.NodePi, Actions: config.Actions{Enabled: true}}
	})
	d := peer.Decision{Kind: "qbit_delete", EntityKey: "sonarr:1"}

	if _, err := a.ReceiveDecision(context.Background(), d); err != nil {
		t.Fatalf("ReceiveDecision() err = %v", err)
	}
	if len(fs.Remediations) != 1 || fs.Remediations[0].Status != "failed" {
		t.Fatalf("Remediations = %+v, want a failed row (no hashes)", fs.Remediations)
	}
}

func TestReceiveDecisionQbitDeleteNilClientRecordsFailed(t *testing.T) {
	a, fs, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Cfg = config.Config{Node: config.NodePi, Actions: config.Actions{Enabled: true}}
		o.Deps = fakeDeps(check.Deps{}, nil) // QBit left nil
	})
	d := peer.Decision{Kind: "qbit_delete", EntityKey: "sonarr:1", Payload: map[string]any{"hashes": []string{"abc"}}}

	if _, err := a.ReceiveDecision(context.Background(), d); err != nil {
		t.Fatalf("ReceiveDecision() err = %v", err)
	}
	if len(fs.Remediations) != 1 || fs.Remediations[0].Status != "failed" || !strings.Contains(fs.Remediations[0].Detail, "not configured") {
		t.Fatalf("Remediations = %+v, want a failed row mentioning not configured", fs.Remediations)
	}
}

func TestReceiveDecisionQbitDeleteBuildDepsErrorRecordsFailed(t *testing.T) {
	wantErr := errors.New("deps boom")
	a, fs, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Cfg = config.Config{Node: config.NodePi, Actions: config.Actions{Enabled: true}}
		o.Deps = fakeDeps(check.Deps{}, wantErr)
	})
	d := peer.Decision{Kind: "qbit_delete", EntityKey: "sonarr:1", Payload: map[string]any{"hashes": []string{"abc"}}}

	if _, err := a.ReceiveDecision(context.Background(), d); err != nil {
		t.Fatalf("ReceiveDecision() err = %v", err)
	}
	if len(fs.Remediations) != 1 || fs.Remediations[0].Status != "failed" || !strings.Contains(fs.Remediations[0].Detail, "deps boom") {
		t.Fatalf("Remediations = %+v, want a failed row mentioning deps boom", fs.Remediations)
	}
}

// TestReceiveDecisionQbitDeleteRecordRemediationFailureIsNotFatal proves
// that a failure recording the remediation row itself (as opposed to the
// qbit_delete attempt) still lets ReceiveDecision return successfully.
func TestReceiveDecisionQbitDeleteRecordRemediationFailureIsNotFatal(t *testing.T) {
	a, _, _ := newHandlerTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{RecordRemediationErr: errors.New("remediation write boom")}
	})
	d := peer.Decision{Kind: "qbit_delete", EntityKey: "sonarr:1"} // actions disabled -> blocked path

	if _, err := a.ReceiveDecision(context.Background(), d); err != nil {
		t.Fatalf("ReceiveDecision() err = %v, want nil despite the remediation write failing", err)
	}
}

func TestHashesFromPayloadHandlesBothWireShapes(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    []string
	}{
		{"in-process []string", map[string]any{"hashes": []string{"a", "b"}}, []string{"a", "b"}},
		{"json-decoded []any", map[string]any{"hashes": []any{"a", "b"}}, []string{"a", "b"}},
		{"json-decoded []any with a non-string entry", map[string]any{"hashes": []any{"a", float64(1)}}, []string{"a"}},
		{"missing key", map[string]any{}, nil},
		{"wrong type", map[string]any{"hashes": "a"}, nil},
	}
	for _, c := range cases {
		if got := hashesFromPayload(c.payload); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: hashesFromPayload(%v) = %v, want %v", c.name, c.payload, got, c.want)
		}
	}
}
