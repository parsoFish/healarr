package notify

import (
	"reflect"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

func TestBuildDigestMergesOwnReportAndPeer(t *testing.T) {
	now := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	stale := now.Add(-3 * time.Hour)

	ownFindings := []check.Finding{
		fixedFinding("mount_race", "smb1", check.SeverityCritical, check.TierCorrect, "SMB mount flapping", "", now),
	}
	own := check.Report{
		Node:         config.NodePi,
		GeneratedAt:  now,
		ChecksRun:    6,
		ChecksFailed: 1,
		Errors:       []check.CheckError{{CheckID: "prowlarr_indexers", Error: "boom"}},
		Skipped:      []string{"tautulli_reachability"},
		Metrics:      map[string]float64{"disk_used_percent:/": 42},
	}
	peer := &PeerSection{
		Node:        config.NodeNAS,
		GeneratedAt: now,
		Findings: []check.Finding{
			fixedFinding("qbit_stalled", "t1", check.SeverityWarn, check.TierNudge, "stalled torrent", "", now),
		},
		ChecksRun:    4,
		ChecksFailed: 0,
		StaleSince:   &stale,
	}

	got := BuildDigest(config.NodePi, now, own, ownFindings, peer, "http://192.0.2.10/healarr")

	if got.Node != config.NodePi {
		t.Fatalf("Node = %v, want pi", got.Node)
	}
	if !got.GeneratedAt.Equal(now) {
		t.Fatalf("GeneratedAt = %v, want %v", got.GeneratedAt, now)
	}
	if !reflect.DeepEqual(got.Findings, ownFindings) {
		t.Fatalf("Findings = %v, want %v", got.Findings, ownFindings)
	}
	if !reflect.DeepEqual(got.Errors, own.Errors) {
		t.Fatalf("Errors = %v, want %v", got.Errors, own.Errors)
	}
	if !reflect.DeepEqual(got.Skipped, own.Skipped) {
		t.Fatalf("Skipped = %v, want %v", got.Skipped, own.Skipped)
	}
	if got.ChecksRun != own.ChecksRun {
		t.Fatalf("ChecksRun = %d, want %d", got.ChecksRun, own.ChecksRun)
	}
	if !reflect.DeepEqual(got.Metrics, own.Metrics) {
		t.Fatalf("Metrics = %v, want %v", got.Metrics, own.Metrics)
	}
	if got.BaseURL != "http://192.0.2.10/healarr" {
		t.Fatalf("BaseURL = %q", got.BaseURL)
	}
	if got.Peer == nil {
		t.Fatal("expected Peer to be carried through")
	}
	if got.Peer.StaleSince == nil || !got.Peer.StaleSince.Equal(stale) {
		t.Fatalf("Peer.StaleSince not preserved: %+v", got.Peer.StaleSince)
	}
	if !reflect.DeepEqual(got.Peer.Findings, peer.Findings) {
		t.Fatalf("Peer.Findings = %v, want %v", got.Peer.Findings, peer.Findings)
	}
}

func TestBuildDigestNilPeerLeavesStaleSinceUnset(t *testing.T) {
	now := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	got := BuildDigest(config.NodeNAS, now, check.Report{ChecksRun: 2}, nil, nil, "")

	if got.Peer != nil {
		t.Fatalf("expected nil Peer, got %+v", got.Peer)
	}
	if got.Node != config.NodeNAS {
		t.Fatalf("Node = %v, want nas", got.Node)
	}
	if got.ChecksRun != 2 {
		t.Fatalf("ChecksRun = %d, want 2", got.ChecksRun)
	}
}

func TestBuildDigestNeverMutatesInputs(t *testing.T) {
	now := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	own := check.Report{Errors: []check.CheckError{{CheckID: "a", Error: "e"}}}
	ownErrorsBefore := append([]check.CheckError(nil), own.Errors...)
	peer := &PeerSection{Node: config.NodeNAS}

	_ = BuildDigest(config.NodePi, now, own, nil, peer, "")

	if !reflect.DeepEqual(own.Errors, ownErrorsBefore) {
		t.Fatalf("own.Errors mutated: %v vs %v", own.Errors, ownErrorsBefore)
	}
	if peer.Node != config.NodeNAS {
		t.Fatalf("peer mutated: %+v", peer)
	}
}
