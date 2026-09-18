package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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

// stalenessScanCheckID is the check id staleness_scan findings carry
// (internal/staleness/check.go); this package has no dependency on that
// package (only on the check.Finding it produces), so it keeps its own
// copy of the literal, mirroring internal/web's.
const stalenessScanCheckID = "staleness_scan"

// digestCleanupWindow bounds how far back SendDigest looks into
// remediations for the CLEANUP section's "latest plan per kind" —
// wide enough to always cover last night's 00:10 cleanup-planning job
// (cleanupjob.go), whatever time the digest itself sends at.
const digestCleanupWindow = 24 * time.Hour

// cleanupPlannedAction/cleanupActionPrefix identify a cleanup dry-run
// plan's remediations row (cleanupjob.go and internal/cli/cmd_cleanup.go
// both record these): Action starts with "cleanup:<kind>" and Status is
// "planned".
const (
	cleanupActionPrefix  = "cleanup:"
	cleanupPlannedStatus = "planned"
)

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
	ownFindingList := toFindings(ownFindings)

	cleanupPlans, err := a.recentCleanupPlans(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("agent: send digest: recent cleanup plans: %w", err)
	}
	pending, err := a.store.PendingDecisions(ctx)
	if err != nil {
		return 0, fmt.Errorf("agent: send digest: pending decisions: %w", err)
	}

	input := notify.BuildDigest(a.cfg.Node, now, own, ownFindingList, peerSection, a.cfg.Web.PublicURL,
		notify.WithStaleness(collectStalenessFindings(ownFindingList, peerSection)),
		notify.WithCleanupPlans(cleanupPlans),
		notify.WithPendingDecisions(len(pending)),
	)
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

// collectStalenessFindings gathers every open staleness_scan finding
// across both nodes — own (already fetched for the digest body) and the
// peer's (from peerSection, when its channel has ever reported one) — and
// sorts them by Data["score"] descending, so the digest's STALENESS
// section highlights the most urgent candidates first. Neither own nor
// peerSection is mutated: a fresh slice is built and sorted, never
// resliced in place.
func collectStalenessFindings(own []check.Finding, peerSection *notify.PeerSection) []check.Finding {
	out := make([]check.Finding, 0, len(own))
	for _, f := range own {
		if f.CheckID == stalenessScanCheckID {
			out = append(out, f)
		}
	}
	if peerSection != nil {
		for _, f := range peerSection.Findings {
			if f.CheckID == stalenessScanCheckID {
				out = append(out, f)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return findingScore(out[i]) > findingScore(out[j]) })
	return out
}

// findingScore reads a staleness_scan finding's Data["score"] (a float64,
// per internal/staleness/check.go's stalenessFinding) defensively: a
// missing or wrong-shaped value sorts as 0 rather than erroring — Data
// crossed a JSON round trip through the store, so this is a real
// boundary, mirroring internal/web's own defensive Data readers.
func findingScore(f check.Finding) float64 {
	v, _ := f.Data["score"].(float64)
	return v
}

// recentCleanupPlans summarises the latest dry-run plan per cleanup kind
// recorded within digestCleanupWindow, for the digest's "CLEANUP
// (dry-run plans)" section.
func (a *Agent) recentCleanupPlans(ctx context.Context, now time.Time) ([]notify.CleanupSummary, error) {
	remediations, err := a.store.RecentRemediations(ctx, now.Add(-digestCleanupWindow))
	if err != nil {
		return nil, err
	}
	return cleanupSummariesFromRemediations(remediations), nil
}

// cleanupSummariesFromRemediations picks out every "cleanup:<kind>"
// remediations row still in status "planned" and keeps only the latest
// one per kind. remediations is assumed newest-first (as
// store.RecentRemediations orders it), so the first "planned" row seen
// for a kind is its latest; earlier (older) rows for the same kind are
// skipped.
func cleanupSummariesFromRemediations(remediations []store.Remediation) []notify.CleanupSummary {
	seen := make(map[string]bool)
	var out []notify.CleanupSummary
	for _, r := range remediations {
		if r.Status != cleanupPlannedStatus || !strings.HasPrefix(r.Action, cleanupActionPrefix) {
			continue
		}
		kind := strings.TrimPrefix(r.Action, cleanupActionPrefix)
		if seen[kind] {
			continue
		}
		seen[kind] = true
		items, bytes := parseCleanupPlanDetail(r.Detail)
		out = append(out, notify.CleanupSummary{Kind: kind, Items: items, Bytes: bytes})
	}
	return out
}

// parseCleanupPlanDetail extracts the item/byte counts cleanupjob.go's
// planOneDailyCleanup formats into a remediations row's Detail ("<n>
// items, <bytes> bytes"). A Detail that doesn't match that exact shape —
// e.g. an older-format row internal/cli/cmd_cleanup.go recorded before
// this format existed — yields 0, 0 rather than an error: the kind and
// the fact that a plan exists still render correctly, only the counts
// are unavailable.
func parseCleanupPlanDetail(detail string) (items int, bytes int64) {
	_, _ = fmt.Sscanf(detail, "%d items, %d bytes", &items, &bytes)
	return items, bytes
}
