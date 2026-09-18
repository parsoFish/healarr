package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/peer"
)

func TestPeerPingMissingPeerURLErrors(t *testing.T) {
	deps, _ := newTestDeps() // deps.PeerClient left nil: falls back to defaultPeerClient
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodePi}, config.Secrets{}, nil // Peer.PeerURL == ""
	}

	_, err := runCLIErr(t, deps, "peer", "ping")
	if err == nil || err.Error() != "peer_url is not configured" {
		t.Fatalf("err = %v, want the exact missing-peer_url message", err)
	}
}

func TestPeerPingReachablePrintsAgeAndCounts(t *testing.T) {
	deps, _, p3 := newPhase3Deps(t)
	sentAt := time.Now().Add(-5 * time.Minute)
	p3.Peer.HasLatest = true
	p3.Peer.Envelope = peer.ReportEnvelope{
		Node:   config.NodeNAS,
		SentAt: sentAt,
		Report: check.Report{
			ChecksRun:    4,
			ChecksFailed: 1,
			Findings:     []check.Finding{{CheckID: "a"}, {CheckID: "b"}},
		},
	}

	out := runCLI(t, deps, "--json", "peer", "ping")

	var got peerPingResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if !got.Reachable {
		t.Errorf("Reachable = false, want true")
	}
	if !got.HasReport {
		t.Errorf("HasReport = false, want true")
	}
	if got.ReportAgeSeconds < 250 || got.ReportAgeSeconds > 350 {
		t.Errorf("ReportAgeSeconds = %v, want ~300 (5m)", got.ReportAgeSeconds)
	}
	if got.ChecksRun != 4 || got.ChecksFailed != 1 || got.Findings != 2 {
		t.Errorf("counts = %+v, want ChecksRun=4 ChecksFailed=1 Findings=2", got)
	}

	wantCalls := []string{"Heartbeat(pi)", "FetchLatest()"}
	if len(p3.Peer.Calls) != len(wantCalls) {
		t.Fatalf("Calls = %v, want %v", p3.Peer.Calls, wantCalls)
	}
	for i, w := range wantCalls {
		if p3.Peer.Calls[i] != w {
			t.Errorf("Calls[%d] = %q, want %q", i, p3.Peer.Calls[i], w)
		}
	}
}

func TestPeerPingUnreachablePrintsErrorAndExitsZero(t *testing.T) {
	deps, _, p3 := newPhase3Deps(t)
	p3.Peer.Err = errors.New("connection refused")

	out, err := runCLIErr(t, deps, "peer", "ping")
	if err != nil {
		t.Fatalf("err = %v, want nil (ping is a diagnostic: it never fails on an unreachable peer)", err)
	}
	if !strings.Contains(out, "connection refused") {
		t.Errorf("output = %q, want it to contain the underlying error", out)
	}
	if !strings.Contains(out, "false") {
		t.Errorf("output = %q, want a false Reachable column", out)
	}
}
