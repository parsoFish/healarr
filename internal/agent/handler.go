package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/peer"
)

// ErrOwnNodeReport is returned by ReceiveReport when the envelope claims
// to be from this node itself — a peer must never report as us.
var ErrOwnNodeReport = errors.New("agent: peer report claims own node")

// PeerHandler adapts a to the peer.Handler contract from internal/peer's
// Task 4 server: ReceiveReport, LatestOwnReport, ReceiveDecision and
// ReceiveHeartbeat below give *Agent that exact method set.
func (a *Agent) PeerHandler() peer.Handler { return a }

// peerHandlerSatisfied is a compile-time assertion that *Agent's method
// set actually matches peer.Handler (belt-and-suspenders alongside
// PeerHandler's return type above).
var _ peer.Handler = (*Agent)(nil)

// ReceiveReport stores env's report as the peer's report — env.Node is
// authoritative for which node it came from, never the report's own Node
// field — and records the envelope as an inbound "report" peer message.
// It rejects env.Node == this node's own node: a peer must never claim to
// be us.
func (a *Agent) ReceiveReport(ctx context.Context, env peer.ReportEnvelope) (int64, error) {
	if env.Node == a.cfg.Node {
		return 0, fmt.Errorf("agent: receive report: %w: %s", ErrOwnNodeReport, env.Node)
	}

	rep := env.Report
	rep.Node = env.Node

	id, _, err := a.store.SaveReport(ctx, rep)
	if err != nil {
		return 0, fmt.Errorf("agent: receive report: save: %w", err)
	}

	if _, err := a.recordInbound(ctx, "report", env.Node, env); err != nil {
		return 0, fmt.Errorf("agent: receive report: record: %w", err)
	}
	return id, nil
}

// LatestOwnReport returns this node's most recently saved report wrapped
// in a ReportEnvelope, so the peer's client can pull it via GET
// /v1/report/latest. ok is false (with a nil error) when nothing has been
// saved yet.
func (a *Agent) LatestOwnReport(ctx context.Context) (peer.ReportEnvelope, bool, error) {
	rep, found, err := a.store.LatestReport(ctx, a.cfg.Node)
	if err != nil {
		return peer.ReportEnvelope{}, false, fmt.Errorf("agent: latest own report: %w", err)
	}
	if !found {
		return peer.ReportEnvelope{}, false, nil
	}
	return peer.ReportEnvelope{Node: a.cfg.Node, SentAt: a.now(), Version: a.version, Report: rep}, true, nil
}

// ReceiveDecision only records d as an inbound "decision" peer message
// (ADR-006, observe-only: Phase 3 never acts on a decision, Phase 4
// executes it). Decision carries no Node field, so the peer identity
// recorded is the deployment's other node.
func (a *Agent) ReceiveDecision(ctx context.Context, d peer.Decision) (int64, error) {
	id, err := a.recordInbound(ctx, "decision", otherNode(a.cfg.Node), d)
	if err != nil {
		return 0, fmt.Errorf("agent: receive decision: %w", err)
	}
	return id, nil
}

// ReceiveHeartbeat records hb as an inbound "heartbeat" peer message.
func (a *Agent) ReceiveHeartbeat(ctx context.Context, hb peer.Heartbeat) error {
	if _, err := a.recordInbound(ctx, "heartbeat", hb.Node, hb); err != nil {
		return fmt.Errorf("agent: receive heartbeat: %w", err)
	}
	return nil
}

// recordInbound marshals payload and records it as an inbound ("in")
// peer message from peerNode of the given kind, stamped with a.now(). A
// marshal failure is logged and swapped for a best-effort fallback
// payload, so the message is still recorded rather than dropped.
func (a *Agent) recordInbound(ctx context.Context, kind string, peerNode config.Node, payload any) (int64, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		a.logger.Error("agent: marshal inbound peer message", "kind", kind, "peer", peerNode, "error", err)
		body = fallbackPayload(err)
	}
	return a.store.SavePeerMessage(ctx, "in", kind, peerNode, body, a.now())
}
