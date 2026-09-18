package agent

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/peer"
)

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
