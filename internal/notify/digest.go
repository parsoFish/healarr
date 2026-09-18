// Package notify renders healarr's plain-text digest from a check run.
// Phase 3 adds the msmtp sender that mails the rendered text; Phase 5
// prepends an LLM narrative ahead of it.
package notify

import (
	_ "embed"
	"fmt"
	"math"
	"strings"
	"text/template"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

//go:embed digest.tmpl
var digestTmplSrc string

// digestTmpl is parsed once at init from the embedded, version-controlled
// template, so a malformed template is a build-time-detectable panic, not
// a runtime error path callers need to handle.
var digestTmpl = template.Must(template.New("digest").Parse(digestTmplSrc))

// timestampLayout matches the digest brief: "2006-01-02 15:04 MST",
// rendered in the report's own time.Location rather than normalised to UTC.
const timestampLayout = "2006-01-02 15:04 MST"

// stalenessTopN bounds the digest's STALENESS section to the highest-
// scoring candidates: an operator skims a morning email, not a full
// report, and DigestInput.Staleness may otherwise carry every open
// staleness_scan finding across both nodes.
const stalenessTopN = 10

// DigestInput is everything the templated digest renders. Phase 3 fills
// BaseURL from the NAS report and Peer from the peer channel; Phase 4 adds
// Staleness/CleanupPlans/PendingDecisions (via BuildDigest's options);
// Phase 5 prepends an LLM narrative.
type DigestInput struct {
	Node        config.Node        `json:"node"`
	GeneratedAt time.Time          `json:"generatedAt"`
	Findings    []check.Finding    `json:"findings"` // open findings, already sorted severity desc
	Errors      []check.CheckError `json:"errors"`
	Skipped     []string           `json:"skipped"`
	ChecksRun   int                `json:"checksRun"`
	Metrics     map[string]float64 `json:"metrics"`
	BaseURL     string             `json:"baseUrl"` // e.g. "http://192.0.2.20/healarr" — may be empty in Phase 2

	// Staleness is every open staleness_scan finding across both nodes,
	// in the order the digest should list them (the caller sorts —
	// BuildDigest never reorders). Only the top stalenessTopN render.
	Staleness []check.Finding `json:"staleness,omitempty"`
	// CleanupPlans is the latest dry-run plan per cleanup kind recorded
	// in the last 24h (internal/agent's cleanupjob.go).
	CleanupPlans []CleanupSummary `json:"cleanupPlans,omitempty"`
	// PendingDecisions is how many decisions are awaiting a human choice.
	PendingDecisions int `json:"pendingDecisions"`

	Peer *PeerSection `json:"peer,omitempty"`
}

// Subject renders the digest email subject line: today's date plus
// critical/warn counts combined across this node's findings and, when
// present, the peer's.
func (in DigestInput) Subject() string {
	critical, warn := countSeverities(in.Findings)
	if in.Peer != nil {
		peerCritical, peerWarn := countSeverities(in.Peer.Findings)
		critical += peerCritical
		warn += peerWarn
	}
	return fmt.Sprintf("healarr digest — %s — %d critical, %d warn",
		in.GeneratedAt.Format("2006-01-02"), critical, warn)
}

// countSeverities tallies critical and warn findings; info findings
// don't affect the subject line.
func countSeverities(findings []check.Finding) (critical, warn int) {
	for _, f := range findings {
		switch f.Severity {
		case check.SeverityCritical:
			critical++
		case check.SeverityWarn:
			warn++
		}
	}
	return critical, warn
}

// otherNode returns the two-node deployment's other node: pi's peer is
// nas and vice versa. Used when Peer is nil, so the digest can still
// name which node never reported.
func otherNode(n config.Node) config.Node {
	if n == config.NodePi {
		return config.NodeNAS
	}
	return config.NodePi
}

// RenderDigest renders the plain-text digest. It never returns an empty
// string on success: with no findings it says so explicitly.
func RenderDigest(in DigestInput) (string, error) {
	var buf strings.Builder
	if err := digestTmpl.Execute(&buf, buildDigestView(in)); err != nil {
		return "", fmt.Errorf("notify: render digest: %w", err)
	}
	return buf.String(), nil
}

// digestView is the template's render model. It is derived from
// DigestInput without mutating it: findings are bucketed by severity in
// their existing order, never re-sorted.
type digestView struct {
	Node          config.Node
	Timestamp     string
	ChecksRun     int
	ChecksFailed  int
	SkippedCount  int
	FindingsCount int
	CriticalCount int
	WarnCount     int
	InfoCount     int
	HasFindings   bool
	Critical      []findingView
	Warn          []findingView
	Info          []findingView
	Errors        []check.CheckError
	SkippedList   string
	BaseURL       string

	PendingDecisions int
	Staleness        []stalenessLineView
	CleanupPlans     []cleanupPlanLineView

	Peer peerView
}

// stalenessLineView is one STALENESS section line item: title, score,
// band and human-readable size.
type stalenessLineView struct {
	Title string
	Score int
	Band  string
	Size  string
}

// cleanupPlanLineView is one "CLEANUP (dry-run plans)" section line item.
type cleanupPlanLineView struct {
	Kind  string
	Items int
	Bytes string
}

// peerView is the template's render model for the PEER (<node>) block.
// Exactly one of Unavailable, Stale, or neither is true; the third case
// is a fresh peer report, rendered the same way as the own-node sections.
type peerView struct {
	Node          config.Node
	Unavailable   bool // Peer == nil: no report has ever been received
	Stale         bool // Peer.StaleSince != nil: the last report is stale
	StaleSince    string
	Timestamp     string
	ChecksRun     int
	ChecksFailed  int
	FindingsCount int
	CriticalCount int
	WarnCount     int
	InfoCount     int
	HasFindings   bool
	Critical      []findingView
	Warn          []findingView
	Info          []findingView
}

// findingView is one `- [checkId] summary` line item under a severity
// section, with its optional indented detail.
type findingView struct {
	CheckID string
	Summary string
	Detail  string
}

// buildDigestView projects a DigestInput into the template's render model.
func buildDigestView(in DigestInput) digestView {
	view := digestView{
		Node:          in.Node,
		Timestamp:     in.GeneratedAt.Format(timestampLayout),
		ChecksRun:     in.ChecksRun,
		ChecksFailed:  len(in.Errors),
		SkippedCount:  len(in.Skipped),
		FindingsCount: len(in.Findings),
		Errors:        in.Errors,
		SkippedList:   strings.Join(in.Skipped, ", "),
		BaseURL:       in.BaseURL,

		PendingDecisions: in.PendingDecisions,
		Staleness:        buildStalenessLines(in.Staleness),
		CleanupPlans:     buildCleanupPlanLines(in.CleanupPlans),

		Peer: buildPeerView(in.Node, in.Peer),
	}
	for _, f := range in.Findings {
		fv := findingView{CheckID: f.CheckID, Summary: f.Summary, Detail: f.Detail}
		switch f.Severity {
		case check.SeverityCritical:
			view.Critical = append(view.Critical, fv)
		case check.SeverityWarn:
			view.Warn = append(view.Warn, fv)
		default:
			// check.SeverityInfo and any unrecognised severity render
			// under INFO rather than being silently dropped.
			view.Info = append(view.Info, fv)
		}
	}
	view.CriticalCount = len(view.Critical)
	view.WarnCount = len(view.Warn)
	view.InfoCount = len(view.Info)
	view.HasFindings = view.FindingsCount > 0
	return view
}

// buildPeerView projects a PeerSection (or its absence) into the
// template's PEER (<node>) render model. selfNode is used to name the
// peer when peer is nil, since PeerSection itself is unavailable then.
func buildPeerView(selfNode config.Node, peer *PeerSection) peerView {
	if peer == nil {
		return peerView{Node: otherNode(selfNode), Unavailable: true}
	}

	view := peerView{
		Node:          peer.Node,
		Timestamp:     peer.GeneratedAt.Format(timestampLayout),
		ChecksRun:     peer.ChecksRun,
		ChecksFailed:  peer.ChecksFailed,
		FindingsCount: len(peer.Findings),
	}
	if peer.StaleSince != nil {
		view.Stale = true
		view.StaleSince = peer.StaleSince.Format(timestampLayout)
	}
	for _, f := range peer.Findings {
		fv := findingView{CheckID: f.CheckID, Summary: f.Summary, Detail: f.Detail}
		switch f.Severity {
		case check.SeverityCritical:
			view.Critical = append(view.Critical, fv)
		case check.SeverityWarn:
			view.Warn = append(view.Warn, fv)
		default:
			view.Info = append(view.Info, fv)
		}
	}
	view.CriticalCount = len(view.Critical)
	view.WarnCount = len(view.Warn)
	view.InfoCount = len(view.Info)
	view.HasFindings = view.FindingsCount > 0
	return view
}

// staleness_scan's two severities (internal/staleness/check.go) map onto
// the C6 band names 1:1: a delete candidate is always warn, a watch-list
// entry always info. Reusing Severity avoids adding a config.Staleness
// dependency here just to recompute the thresholds that already decided
// it.
const (
	stalenessBandCandidate = "candidate"
	stalenessBandWatchlist = "watchlist"
)

// buildStalenessLines projects up to stalenessTopN findings (already
// sorted by the caller) into the STALENESS section's render model. Every
// field is read defensively from Finding.Data — it crossed a JSON round
// trip through the store, so a missing or wrong-shaped key renders as its
// zero value rather than panicking, mirroring internal/web's
// toStalenessView.
func buildStalenessLines(findings []check.Finding) []stalenessLineView {
	n := len(findings)
	if n > stalenessTopN {
		n = stalenessTopN
	}
	out := make([]stalenessLineView, 0, n)
	for _, f := range findings[:n] {
		out = append(out, stalenessLineView{
			Title: dataString(f.Data, "title"),
			Score: int(math.Round(dataFloat(f.Data, "score"))),
			Band:  stalenessBand(f.Severity),
			Size:  humanizeBytes(int64(dataFloat(f.Data, "sizeBytes"))),
		})
	}
	return out
}

// stalenessBand maps a staleness_scan finding's severity to its C6 band
// name; any other severity (never emitted by that check, but handled
// rather than panicking) reports as the watchlist band.
func stalenessBand(sev check.Severity) string {
	if sev == check.SeverityWarn {
		return stalenessBandCandidate
	}
	return stalenessBandWatchlist
}

// buildCleanupPlanLines projects CleanupSummary rows into the "CLEANUP
// (dry-run plans)" section's render model, humanising each plan's bytes.
func buildCleanupPlanLines(plans []CleanupSummary) []cleanupPlanLineView {
	out := make([]cleanupPlanLineView, 0, len(plans))
	for _, p := range plans {
		out = append(out, cleanupPlanLineView{Kind: p.Kind, Items: p.Items, Bytes: humanizeBytes(p.Bytes)})
	}
	return out
}

// dataFloat/dataString read a check.Finding.Data map defensively: a
// missing key or a value of the wrong type (Data crossed a JSON round
// trip through the store, so this is a real boundary) renders as the
// type's zero value rather than panicking.
func dataFloat(data map[string]any, key string) float64 {
	f, _ := data[key].(float64)
	return f
}

func dataString(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return s
}

// humanizeBytes renders n as a binary-prefixed size ("12.0 GiB"),
// clamping a negative value to 0 rather than printing a nonsense size.
// Hand-rolled (like internal/web's identical helper) rather than pulling
// in a formatting library for a handful of lines (ADR-013).
func humanizeBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
