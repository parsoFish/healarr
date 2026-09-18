package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/decision"
	"github.com/parsoFish/healarr/internal/staleness"
	"github.com/parsoFish/healarr/internal/store"
)

// stalenessCheckID is the check id staleness_scan findings carry
// (internal/staleness/check.go); the decisions page shows only these.
const stalenessCheckID = "staleness_scan"

// decisionOutcomeAction is the Action a delete decision's remediation
// row is recorded under (internal/decision/execute.go's
// recordDeleteRemediation), used below to tell "blocked"/"executed"
// decision outcomes apart from cleanup plans in the same table.
const decisionOutcomeAction = "decision_delete"

// cleanupActionPrefix is the Action prefix the cleanup CLI/daily job
// records dry-run plans under (internal/cli/cmd_cleanup.go).
const cleanupActionPrefix = "cleanup:"

// snoozedStatus is store.StoredFinding.Status's value for a finding the
// operator has snoozed (Keep, below, or `healarr decide keep`).
// OpenFindings returns status open and snoozed together; a snoozed
// finding must not render as a decision candidate — see
// stalenessCandidates and buildNodeView's identical filter in
// handlers_dashboard.go.
const snoozedStatus = "snoozed"

// recentActivityWindow bounds how far back the decisions and history
// pages look into remediations for recent activity.
const recentActivityWindow = 7 * 24 * time.Hour

// entityKeyPattern is the only shape CreateDecision may be asked to
// record: a Sonarr or Radarr internal id, matching what
// internal/decision's parseEntityKey accepts.
var entityKeyPattern = regexp.MustCompile(`^(sonarr|radarr):\d+$`)

type decisionsView struct {
	PageData
	ActionsEnabled bool
	Candidates     []stalenessView
	Pending        []decisionRowView
	Blocked        []remediationView
	Executed       []remediationView
	CleanupPlans   []remediationView
}

type stalenessView struct {
	EntityKey   string
	Title       string
	Kind        string
	Score       float64
	Band        string
	Components  []componentView
	SizeBytes   int64
	LastWatched time.Time
	HasWatched  bool
	RequestedBy string
}

type componentView struct {
	Name  string
	Value float64
}

type decisionRowView struct {
	ID          int64
	EntityKey   string
	Kind        string
	Status      string
	RequestedAt time.Time
}

type remediationView struct {
	Action    string
	Node      string
	Status    string
	Detail    string
	CreatedAt time.Time
	DryRun    bool
}

// handleDecisionsGet renders "/decisions": staleness candidates sorted
// score desc, pending decisions, recent blocked/executed delete outcomes
// (derived from remediations — see the Blocked/Executed comment below),
// and read-only cleanup plans.
func (s *server) handleDecisionsGet(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r.Context())
	ctx := r.Context()

	candidates, err := s.stalenessCandidates(ctx)
	if err != nil {
		s.logger.Error("web: decisions: staleness candidates", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	pending, err := s.store.PendingDecisions(ctx)
	if err != nil {
		s.logger.Error("web: decisions: pending decisions", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	since := time.Now().Add(-recentActivityWindow)
	remediations, err := s.store.RecentRemediations(ctx, since)
	if err != nil {
		s.logger.Error("web: decisions: recent remediations", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.render(w, r, "decisions", decisionsView{
		PageData:       s.pageData(sess, r),
		ActionsEnabled: s.cfg.Actions.Enabled,
		Candidates:     candidates,
		Pending:        toDecisionRows(pending),
		// Blocked/Executed: the Store interface this page reads (see
		// server.go) has no "list decisions by status" method beyond
		// PendingDecisions (pending only) — only CreateDecision,
		// PendingDecisions and DecisionByID. Delete decisions record a
		// remediations row on every outcome (decision.Execute's
		// recordDeleteRemediation), so that table is this page's only
		// durable trail for what happened to a delete. A "keep"
		// decision never writes one (constraints.md: only delete
		// attempts are recorded there), so an instantly-executed keep
		// won't appear in Executed below — a real gap, not an
		// oversight; closing it needs a new store method, out of
		// scope for this task.
		Blocked:      filterRemediations(remediations, decisionOutcomeAction, "blocked"),
		Executed:     filterRemediations(remediations, decisionOutcomeAction, "executed"),
		CleanupPlans: filterCleanupPlans(remediations),
	})
}

// handleDecisionsPost handles "POST /decisions": it validates kind and
// entity_key, records a pending decision, and immediately asks the
// runner to execute it. A gated delete (decision.ErrActionsDisabled)
// redirects with a "blocked" flash rather than failing the request —
// the decision was still correctly recorded, just not carried out.
func (s *server) handleDecisionsPost(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.validCSRF(sess, r.PostFormValue("csrf")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	kind := r.PostFormValue("kind")
	entityKey := r.PostFormValue("entity_key")
	if kind != string(decision.KindKeep) && kind != string(decision.KindDelete) {
		http.Error(w, "invalid kind", http.StatusBadRequest)
		return
	}
	if !entityKeyPattern.MatchString(entityKey) {
		http.Error(w, "invalid entity_key", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	id, err := s.store.CreateDecision(ctx, entityKey, kind, time.Now())
	if err != nil {
		s.logger.Error("web: create decision", "entity_key", entityKey, "kind", kind, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if _, err := s.runner.Execute(ctx, id); err != nil {
		if errors.Is(err, decision.ErrActionsDisabled) {
			http.Redirect(w, r, s.basePath+"/decisions?flash=blocked", http.StatusSeeOther)
			return
		}
		s.logger.Error("web: execute decision", "decision_id", id, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, s.basePath+"/decisions?flash=ok", http.StatusSeeOther)
}

// stalenessCandidates collects staleness_scan findings across both
// nodes (the check only ever runs on pi per the C5 catalogue, but this
// doesn't hardcode that assumption) and sorts them score desc, entity
// key as a deterministic tiebreak.
func (s *server) stalenessCandidates(ctx context.Context) ([]stalenessView, error) {
	var out []stalenessView
	for _, node := range dashboardNodes {
		findings, err := s.store.OpenFindings(ctx, node)
		if err != nil {
			return nil, fmt.Errorf("open findings %s: %w", node, err)
		}
		for _, f := range findings {
			if f.CheckID != stalenessCheckID || f.Status == snoozedStatus {
				continue
			}
			out = append(out, toStalenessView(f.EntityKey, f.Data, s.cfg.Staleness))
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].EntityKey < out[j].EntityKey
	})
	return out, nil
}

// toStalenessView reads a staleness_scan finding's Data map (see
// internal/staleness/check.go's stalenessFinding: score, components,
// sizeBytes, lastWatched, requestedBy, title, kind) into display form.
// Every field is read defensively — a missing or wrong-shaped key
// renders as its zero value rather than panicking, since Data crossed a
// JSON round trip through the store and this is a boundary.
func toStalenessView(entityKey string, data map[string]any, w config.Staleness) stalenessView {
	score := dataFloat(data, "score")
	v := stalenessView{
		EntityKey:   entityKey,
		Title:       dataString(data, "title"),
		Kind:        dataString(data, "kind"),
		Score:       score,
		Band:        staleness.Band(score, w),
		Components:  dataComponents(data),
		SizeBytes:   int64(dataFloat(data, "sizeBytes")),
		RequestedBy: dataString(data, "requestedBy"),
	}
	if lw, ok := dataTime(data, "lastWatched"); ok {
		v.LastWatched = lw
		v.HasWatched = true
	}
	return v
}

func dataFloat(data map[string]any, key string) float64 {
	f, _ := data[key].(float64)
	return f
}

func dataString(data map[string]any, key string) string {
	s, _ := data[key].(string)
	return s
}

func dataTime(data map[string]any, key string) (time.Time, bool) {
	s, ok := data[key].(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.IsZero() {
		return time.Time{}, false
	}
	return t, true
}

func dataComponents(data map[string]any) []componentView {
	raw, ok := data["components"].(map[string]any)
	if !ok {
		return nil
	}
	out := make([]componentView, 0, len(raw))
	for name, v := range raw {
		f, _ := v.(float64)
		out = append(out, componentView{Name: name, Value: f})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func toDecisionRows(decisions []store.Decision) []decisionRowView {
	out := make([]decisionRowView, 0, len(decisions))
	for _, d := range decisions {
		out = append(out, decisionRowView{
			ID:          d.ID,
			EntityKey:   d.EntityKey,
			Kind:        d.Kind,
			Status:      d.Status,
			RequestedAt: d.RequestedAt,
		})
	}
	return out
}

func filterRemediations(remediations []store.Remediation, action, status string) []remediationView {
	var out []remediationView
	for _, r := range remediations {
		if r.Action == action && r.Status == status {
			out = append(out, toRemediationView(r))
		}
	}
	return out
}

// filterCleanupPlans lists remediations whose Action starts with
// "cleanup:" (internal/cli/cmd_cleanup.go's recordCleanup) — read-only
// here; execution buttons for these plans are out of scope for this
// task (see task-6-brief.md) and are left to a later one.
func filterCleanupPlans(remediations []store.Remediation) []remediationView {
	var out []remediationView
	for _, r := range remediations {
		if strings.HasPrefix(r.Action, cleanupActionPrefix) {
			out = append(out, toRemediationView(r))
		}
	}
	return out
}

func toRemediationView(r store.Remediation) remediationView {
	return remediationView{
		Action:    r.Action,
		Node:      string(r.Node),
		Status:    r.Status,
		Detail:    r.Detail,
		CreatedAt: r.CreatedAt,
		DryRun:    r.DryRun,
	}
}
