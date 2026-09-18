package web

import (
	"net/http"
	"time"

	"github.com/parsoFish/healarr/internal/store"
)

type historyView struct {
	PageData
	Nodes        []nodeHistoryView
	Remediations []remediationView
}

type nodeHistoryView struct {
	Node     string
	Findings []findingHistoryView
}

type findingHistoryView struct {
	CheckID   string
	EntityKey string
	Summary   string
	Status    string
	LastSeen  time.Time
}

// historyLimit caps how many finding-history rows each node's query
// returns to the page, per the brief's "FindingHistory(both nodes, 7 d,
// 200)".
const historyLimit = 200

// handleHistory renders "/history": each node's finding history over
// the trailing week, plus recent remediations (cleanup and decision
// activity alike — unlike /decisions, this page doesn't filter by
// Action, since its job is a general activity log).
func (s *server) handleHistory(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r.Context())
	ctx := r.Context()
	since := time.Now().Add(-recentActivityWindow)

	nodes := make([]nodeHistoryView, 0, len(dashboardNodes))
	for _, node := range dashboardNodes {
		findings, err := s.store.FindingHistory(ctx, node, since, historyLimit)
		if err != nil {
			s.logger.Error("web: history: finding history", "node", node, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		nodes = append(nodes, nodeHistoryView{Node: string(node), Findings: toFindingHistoryViews(findings)})
	}

	remediations, err := s.store.RecentRemediations(ctx, since)
	if err != nil {
		s.logger.Error("web: history: recent remediations", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	out := make([]remediationView, 0, len(remediations))
	for _, r := range remediations {
		out = append(out, toRemediationView(r))
	}

	s.render(w, r, "history", historyView{
		PageData:     s.pageData(sess, r),
		Nodes:        nodes,
		Remediations: out,
	})
}

func toFindingHistoryViews(findings []store.StoredFinding) []findingHistoryView {
	out := make([]findingHistoryView, 0, len(findings))
	for _, f := range findings {
		out = append(out, findingHistoryView{
			CheckID:   f.CheckID,
			EntityKey: f.EntityKey,
			Summary:   f.Summary,
			Status:    f.Status,
			LastSeen:  f.LastSeen,
		})
	}
	return out
}

// loginView is declared here rather than auth.go so every page's view
// struct lives alongside the others; auth.go stays focused on session
// and CSRF mechanics.
type loginView struct {
	PageData
}
