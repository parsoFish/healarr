package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// dashboardNodes is the fixed pair of nodes ADR-015 defines: the Pi
// (which also serves this UI) and the NAS peer. The dashboard always
// shows both rows, even when one node has never reported.
var dashboardNodes = []config.Node{config.NodePi, config.NodeNAS}

// diskMetricPrefixes are the check.Report.Metrics key prefixes the
// dashboard reads for disk gauges (see internal/checks/disk and
// internal/checks/mounts); the suffix after the colon is the mount path.
var diskMetricPrefixes = []string{"disk_used_percent:", "mount_used_percent:"}

type dashboardView struct {
	PageData
	Nodes []nodeView
}

type nodeView struct {
	Node          string
	HasReport     bool
	GeneratedAt   time.Time
	ChecksRun     int
	ChecksFailed  int
	FindingsCount int
	HasHeartbeat  bool
	HeartbeatAt   time.Time
	DiskGauges    []diskGauge
	Critical      []findingView
	Warn          []findingView
	Info          []findingView
}

type diskGauge struct {
	Label   string
	Percent float64
}

type findingView struct {
	CheckID   string
	EntityKey string
	Summary   string
	Detail    string
}

// handleDashboard renders "/": for each of pi and nas, its latest
// report's timing/counts, peer heartbeat age, disk gauges and open
// findings grouped by severity.
func (s *server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r.Context())
	ctx := r.Context()

	nodes := make([]nodeView, 0, len(dashboardNodes))
	for _, node := range dashboardNodes {
		nv, err := s.buildNodeView(ctx, node)
		if err != nil {
			s.logger.Error("web: dashboard: build node view", "node", node, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		nodes = append(nodes, nv)
	}

	s.render(w, r, "dashboard", dashboardView{
		PageData: s.pageData(sess, r),
		Nodes:    nodes,
	})
}

func (s *server) buildNodeView(ctx context.Context, node config.Node) (nodeView, error) {
	nv := nodeView{Node: string(node)}

	rep, ok, err := s.store.LatestReport(ctx, node)
	if err != nil {
		return nodeView{}, fmt.Errorf("latest report: %w", err)
	}
	if ok {
		nv.HasReport = true
		nv.GeneratedAt = rep.GeneratedAt
		nv.ChecksRun = rep.ChecksRun
		nv.ChecksFailed = rep.ChecksFailed
		nv.DiskGauges = diskGauges(rep.Metrics)
	}

	heartbeatAt, hbOK, err := s.store.LastPeerMessageAt(ctx, node, "heartbeat")
	if err != nil {
		return nodeView{}, fmt.Errorf("last heartbeat: %w", err)
	}
	nv.HasHeartbeat = hbOK
	nv.HeartbeatAt = heartbeatAt

	findings, err := s.store.OpenFindings(ctx, node)
	if err != nil {
		return nodeView{}, fmt.Errorf("open findings: %w", err)
	}
	// OpenFindings returns status open and snoozed together; the
	// dashboard's "N open findings" count (and the severity breakdown it
	// gates) must reflect only what's actually open, not what the
	// operator has already acknowledged and snoozed — see
	// stalenessCandidates' identical filter in handlers_decisions.go.
	findings = excludeSnoozed(findings)
	nv.FindingsCount = len(findings)
	nv.Critical, nv.Warn, nv.Info = groupBySeverity(findings)

	return nv, nil
}

// diskGauges extracts disk_used_percent:*/mount_used_percent:* entries
// from metrics into a deterministically ordered (by label) slice.
func diskGauges(metrics map[string]float64) []diskGauge {
	var out []diskGauge
	for key, val := range metrics {
		for _, prefix := range diskMetricPrefixes {
			if label, ok := strings.CutPrefix(key, prefix); ok {
				out = append(out, diskGauge{Label: label, Percent: val})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// excludeSnoozed drops snoozed rows from findings (OpenFindings returns
// open and snoozed together), without mutating findings itself.
func excludeSnoozed(findings []store.StoredFinding) []store.StoredFinding {
	out := make([]store.StoredFinding, 0, len(findings))
	for _, f := range findings {
		if f.Status == snoozedStatus {
			continue
		}
		out = append(out, f)
	}
	return out
}

// groupBySeverity splits findings into critical/warn/info buckets for
// display, preserving each StoredFinding's own summary/detail.
func groupBySeverity(findings []store.StoredFinding) (critical, warn, info []findingView) {
	for _, f := range findings {
		fv := findingView{CheckID: f.CheckID, EntityKey: f.EntityKey, Summary: f.Summary, Detail: f.Detail}
		switch f.Severity {
		case check.SeverityCritical:
			critical = append(critical, fv)
		case check.SeverityWarn:
			warn = append(warn, fv)
		default:
			info = append(info, fv)
		}
	}
	return critical, warn, info
}
