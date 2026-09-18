package notify

import (
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// PeerSection is the peer node's contribution to the digest. When the
// daemon (Task 6) has a fresh report from the peer channel, StaleSince is
// nil and Findings/ChecksRun/ChecksFailed describe that report. When the
// peer channel hasn't produced a fresh report recently, StaleSince
// records when the last-known report was generated so the digest can say
// "stale since <time>" instead of presenting old data as current.
type PeerSection struct {
	Node         config.Node     `json:"node"`
	GeneratedAt  time.Time       `json:"generatedAt"`
	Findings     []check.Finding `json:"findings"`
	ChecksRun    int             `json:"checksRun"`
	ChecksFailed int             `json:"checksFailed"`
	StaleSince   *time.Time      `json:"staleSince,omitempty"`
}

// CleanupSummary is one cleanup kind's most recent dry-run plan (the
// daily job's own remediations rows; see internal/agent's cleanupjob.go),
// as shown in the digest's "CLEANUP (dry-run plans)" section.
type CleanupSummary struct {
	Kind  string
	Items int
	Bytes int64
}

// DigestOption customises a DigestInput BuildDigest produces, beyond its
// required positional parameters. Phase 4 adds these three (staleness
// candidates, cleanup plans, pending decisions) as options rather than
// positional parameters so every existing BuildDigest call site keeps
// compiling unchanged.
type DigestOption func(*DigestInput)

// WithStaleness sets the digest's STALENESS section from findings (open
// staleness_scan findings across both nodes; the caller sorts them —
// BuildDigest does no I/O and imposes no ordering of its own).
func WithStaleness(findings []check.Finding) DigestOption {
	return func(in *DigestInput) { in.Staleness = findings }
}

// WithCleanupPlans sets the digest's "CLEANUP (dry-run plans)" section.
func WithCleanupPlans(plans []CleanupSummary) DigestOption {
	return func(in *DigestInput) { in.CleanupPlans = plans }
}

// WithPendingDecisions sets the digest's "Decisions pending: N" line.
func WithPendingDecisions(n int) DigestOption {
	return func(in *DigestInput) { in.PendingDecisions = n }
}

// BuildDigest is the pure merge step between one node's own check run and
// the peer's last-known section into the DigestInput RenderDigest
// expects. It does no I/O and mutates none of its arguments, so it can be
// tested without a store: the daemon fetches own and peer separately and
// calls this to assemble what gets mailed. opts apply on top of the
// required parameters (see WithStaleness, WithCleanupPlans,
// WithPendingDecisions).
func BuildDigest(node config.Node, now time.Time, own check.Report, ownFindings []check.Finding, peer *PeerSection, baseURL string, opts ...DigestOption) DigestInput {
	in := DigestInput{
		Node:        node,
		GeneratedAt: now,
		Findings:    ownFindings,
		Errors:      own.Errors,
		Skipped:     own.Skipped,
		ChecksRun:   own.ChecksRun,
		Metrics:     own.Metrics,
		BaseURL:     baseURL,
		Peer:        peer,
	}
	for _, opt := range opts {
		opt(&in)
	}
	return in
}
