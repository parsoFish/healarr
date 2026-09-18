package peer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/parsoFish/healarr/internal/config"
)

func TestFakeRecordsCallsAndReturnsConfiguredValues(t *testing.T) {
	f := &Fake{Ack: Ack{Status: "ok", ID: 3}, Envelope: ReportEnvelope{Node: config.NodePi}, HasLatest: true}

	ack, err := f.PushReport(context.Background(), ReportEnvelope{Node: config.NodeNAS})
	if err != nil || ack != f.Ack {
		t.Fatalf("PushReport: ack=%+v err=%v", ack, err)
	}

	env, ok, err := f.FetchLatest(context.Background())
	if err != nil || !ok || !reflect.DeepEqual(env, f.Envelope) {
		t.Fatalf("FetchLatest: env=%+v ok=%v err=%v", env, ok, err)
	}

	ack, err = f.SendDecision(context.Background(), Decision{Kind: "nudge", EntityKey: "x"})
	if err != nil || ack != f.Ack {
		t.Fatalf("SendDecision: ack=%+v err=%v", ack, err)
	}

	ack, err = f.Heartbeat(context.Background(), Heartbeat{Node: config.NodePi})
	if err != nil || ack != f.Ack {
		t.Fatalf("Heartbeat: ack=%+v err=%v", ack, err)
	}

	want := []string{
		"PushReport(nas)",
		"FetchLatest()",
		"SendDecision(nudge,x)",
		"Heartbeat(pi)",
	}
	if len(f.Calls) != len(want) {
		t.Fatalf("Calls = %v, want %v", f.Calls, want)
	}
	for i, c := range want {
		if f.Calls[i] != c {
			t.Fatalf("Calls[%d] = %q, want %q", i, f.Calls[i], c)
		}
	}
}

func TestFakeReturnsConfiguredErrFromEveryMethod(t *testing.T) {
	wantErr := errors.New("boom")
	f := &Fake{Err: wantErr}

	if _, err := f.PushReport(context.Background(), ReportEnvelope{}); !errors.Is(err, wantErr) {
		t.Fatalf("PushReport err = %v", err)
	}
	if _, ok, err := f.FetchLatest(context.Background()); ok || !errors.Is(err, wantErr) {
		t.Fatalf("FetchLatest ok=%v err=%v", ok, err)
	}
	if _, err := f.SendDecision(context.Background(), Decision{}); !errors.Is(err, wantErr) {
		t.Fatalf("SendDecision err = %v", err)
	}
	if _, err := f.Heartbeat(context.Background(), Heartbeat{}); !errors.Is(err, wantErr) {
		t.Fatalf("Heartbeat err = %v", err)
	}
}
