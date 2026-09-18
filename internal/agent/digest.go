package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/notify"
	"github.com/parsoFish/healarr/internal/store"
)

// ErrNoSender is returned by SendDigest when this agent has no Sender
// configured (Options.Sender == nil — the NAS, per constraints.md: "the
// NAS never calls the sender"). Only the Pi's scheduled digest job
// (scheduleDigest) ever calls SendDigest, so this only fires on a
// misconfigured or direct call.
var ErrNoSender = errors.New("agent: no sender configured")

// SendDigest builds the daily digest from this node's own latest report
// and open findings plus the peer's (peer section built by
// buildPeerSection), renders it, enqueues it in the outbox, sends it, and
// marks the outbox row sent or failed accordingly. It always returns the
// outbox row's id once EnqueueEmail has succeeded, even when the send
// itself fails, so the caller (and the outbox) can see exactly which row
// is at fault.
func (a *Agent) SendDigest(ctx context.Context) (int64, error) {
	if !a.HasSender() {
		return 0, fmt.Errorf("agent: send digest: %w", ErrNoSender)
	}

	own, _, err := a.store.LatestReport(ctx, a.cfg.Node)
	if err != nil {
		return 0, fmt.Errorf("agent: send digest: own latest report: %w", err)
	}
	ownFindings, err := a.store.OpenFindings(ctx, a.cfg.Node)
	if err != nil {
		return 0, fmt.Errorf("agent: send digest: own open findings: %w", err)
	}
	peerSection, err := a.buildPeerSection(ctx)
	if err != nil {
		return 0, fmt.Errorf("agent: send digest: %w", err)
	}

	now := a.now()
	// BaseURL is left empty: Phase 4 adds the web UI's URL to the digest.
	input := notify.BuildDigest(a.cfg.Node, now, own, toFindings(ownFindings), peerSection, "")
	body, err := notify.RenderDigest(input)
	if err != nil {
		return 0, fmt.Errorf("agent: send digest: render: %w", err)
	}

	id, err := a.store.EnqueueEmail(ctx, a.cfg.Email.To, input.Subject(), body, now)
	if err != nil {
		return 0, fmt.Errorf("agent: send digest: enqueue: %w", err)
	}

	if sendErr := a.sender.Send(ctx, a.cfg.Email.To, input.Subject(), body); sendErr != nil {
		if markErr := a.store.MarkEmailFailed(ctx, id, a.now(), sendErr); markErr != nil {
			a.logger.Error("agent: send digest: mark failed", "id", id, "error", markErr)
		}
		return id, fmt.Errorf("agent: send digest: send: %w", sendErr)
	}

	if err := a.store.MarkEmailSent(ctx, id, a.now()); err != nil {
		a.logger.Error("agent: send digest: mark sent", "id", id, "error", err)
	}
	a.logger.Info("agent: digest sent", "outbox_id", id, "subject", input.Subject())
	return id, nil
}

// buildPeerSection assembles the peer's contribution to the digest from
// this node's own store (the peer's reports/findings are pushed here over
// the peer channel; see cycle.go/handler.go). It returns a nil section
// (Peer == nil in the render) when this node has never received a report
// from the peer at all; otherwise it returns a section whose StaleSince is
// non-nil exactly when the last received report is older than
// Agent.PeerStaleAfter.
func (a *Agent) buildPeerSection(ctx context.Context) (*notify.PeerSection, error) {
	peerNode := otherNode(a.cfg.Node)

	lastReportAt, found, err := a.store.LastPeerMessageAt(ctx, peerNode, "report")
	if err != nil {
		return nil, fmt.Errorf("peer last message: %w", err)
	}
	if !found {
		return nil, nil
	}

	rep, repFound, err := a.store.LatestReport(ctx, peerNode)
	if err != nil {
		return nil, fmt.Errorf("peer latest report: %w", err)
	}
	peerFindings, err := a.store.OpenFindings(ctx, peerNode)
	if err != nil {
		return nil, fmt.Errorf("peer open findings: %w", err)
	}

	section := &notify.PeerSection{
		Node:         peerNode,
		Findings:     toFindings(peerFindings),
		ChecksRun:    rep.ChecksRun,
		ChecksFailed: rep.ChecksFailed,
	}
	if repFound {
		section.GeneratedAt = rep.GeneratedAt
	}
	if a.now().Sub(lastReportAt) > a.cfg.Agent.PeerStaleAfter {
		staleSince := lastReportAt
		section.StaleSince = &staleSince
	}
	return section, nil
}

// toFindings extracts the check.Finding each StoredFinding embeds, without
// mutating stored.
func toFindings(stored []store.StoredFinding) []check.Finding {
	out := make([]check.Finding, 0, len(stored))
	for _, sf := range stored {
		out = append(out, sf.Finding)
	}
	return out
}
