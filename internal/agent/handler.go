package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/decision"
	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
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

// ReceiveDecision records d as an inbound "decision" peer message
// (ADR-006). Decision carries no Node field, so the peer identity
// recorded is the deployment's other node. Phase 4 adds one action on
// top of that record: a "qbit_delete" decision also tells this node's
// qBittorrent to delete the named torrents (executeQbitDelete), gated
// behind cfg.Actions.Enabled like every other mutating path. That
// follow-through never surfaces as an error here — only a failure to
// record the inbound message itself does — so a problem executing it
// can't make the Pi retry redelivering (and re-attempting) the same
// decision; its outcome is instead durably recorded as a remediations
// row and logged.
func (a *Agent) ReceiveDecision(ctx context.Context, d peer.Decision) (int64, error) {
	id, err := a.recordInbound(ctx, "decision", otherNode(a.cfg.Node), d)
	if err != nil {
		return 0, fmt.Errorf("agent: receive decision: %w", err)
	}
	if d.Kind == "qbit_delete" {
		a.executeQbitDelete(ctx, d)
	}
	return id, nil
}

// executeQbitDelete performs the client action a "qbit_delete" decision
// requests: deleting the torrents named in d.Payload's "hashes" via this
// node's qBittorrent client. The actions gate is checked before any
// client write (constraints.md); every outcome (blocked/executed/failed)
// is recorded as a remediations row via recordQbitDeleteRemediation.
func (a *Agent) executeQbitDelete(ctx context.Context, d peer.Decision) {
	now := a.now()
	if !a.cfg.Actions.Enabled {
		a.logger.Warn("agent: qbit_delete blocked: actions disabled", "entity_key", d.EntityKey)
		a.recordQbitDeleteRemediation(ctx, now, "blocked", decision.ErrActionsDisabled.Error())
		return
	}

	hashes := hashesFromPayload(d.Payload)
	if len(hashes) == 0 {
		a.logger.Error("agent: qbit_delete: no hashes in payload", "entity_key", d.EntityKey)
		a.recordQbitDeleteRemediation(ctx, now, "failed", "qbit_delete: no hashes in payload")
		return
	}

	deps, err := a.deps(ctx)
	if err != nil {
		a.logger.Error("agent: qbit_delete: build deps", "entity_key", d.EntityKey, "error", err)
		a.recordQbitDeleteRemediation(ctx, now, "failed", fmt.Sprintf("build deps: %v", err))
		return
	}
	if deps.QBit == nil {
		a.logger.Error("agent: qbit_delete: qbittorrent not configured", "entity_key", d.EntityKey)
		a.recordQbitDeleteRemediation(ctx, now, "failed", check.ErrNotConfigured.Error())
		return
	}

	if err := deps.QBit.Delete(ctx, hashes, true); err != nil {
		a.logger.Error("agent: qbit_delete: delete", "entity_key", d.EntityKey, "error", err)
		a.recordQbitDeleteRemediation(ctx, now, "failed", err.Error())
		return
	}
	a.recordQbitDeleteRemediation(ctx, now, "executed", fmt.Sprintf("deleted %d torrent(s) for %s", len(hashes), d.EntityKey))
}

// recordQbitDeleteRemediation records one remediations row for a
// qbit_delete attempt. A failure to record is only logged — never
// fatal — since the outcome it would have recorded is already decided
// and logged by executeQbitDelete's caller.
func (a *Agent) recordQbitDeleteRemediation(ctx context.Context, at time.Time, status, detail string) {
	r := store.Remediation{
		Node:       a.cfg.Node,
		Action:     "qbit_delete",
		Tier:       string(check.TierCorrect),
		Status:     status,
		Detail:     detail,
		DryRun:     false,
		CreatedAt:  at,
		FinishedAt: &at,
	}
	if _, err := a.store.RecordRemediation(ctx, r); err != nil {
		a.logger.Error("agent: record qbit_delete remediation", "status", status, "error", err)
	}
}

// hashesFromPayload extracts the "hashes" entry from a qbit_delete
// decision's Payload. It handles both shapes that entry arrives in: a
// []string, when Payload is built and consumed in-process (as
// internal/decision's own tests do), and a []any of strings, which is
// what decoding JSON into a map[string]any always produces over the
// wire (encoding/json has no way to know the array held strings ahead
// of time). Anything else — or a missing "hashes" key — yields no
// hashes, never an error; the caller treats "no hashes" as its own
// failure to act on, not a parse error.
func hashesFromPayload(payload map[string]any) []string {
	switch v := payload["hashes"].(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
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
