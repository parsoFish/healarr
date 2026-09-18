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

// BuildDigest is the pure merge step between one node's own check run and
// the peer's last-known section into the DigestInput RenderDigest
// expects. It does no I/O and mutates none of its arguments, so it can be
// tested without a store: the daemon fetches own and peer separately and
// calls this to assemble what gets mailed.
func BuildDigest(node config.Node, now time.Time, own check.Report, ownFindings []check.Finding, peer *PeerSection, baseURL string) DigestInput {
	return DigestInput{
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
}
