package web

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/decision"
	"github.com/parsoFish/healarr/internal/store"
)

func stalenessFixture(entityKey, title string, score float64) store.StoredFinding {
	return store.StoredFinding{
		Finding: check.Finding{
			CheckID:   stalenessCheckID,
			Node:      config.NodePi,
			EntityKey: entityKey,
			Severity:  check.SeverityWarn,
			Summary:   title + " stale",
			Data: map[string]any{
				"score":       score,
				"components":  map[string]any{"days": 30.0, "size": 5.0},
				"sizeBytes":   float64(12_000_000_000),
				"lastWatched": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
				"requestedBy": "example-requester",
				"title":       title,
				"kind":        "series",
			},
		},
	}
}

func TestDecisionsPageRendersCandidatesSortedByScoreDesc(t *testing.T) {
	fs := &fakeStore{
		OpenFindingsByNode: map[config.Node][]store.StoredFinding{
			config.NodePi: {
				stalenessFixture("sonarr:1", "Low Score Show", 55),
				stalenessFixture("sonarr:2", "High Score Show", 92),
			},
		},
	}

	status, body := getAuthenticatedPage(t, fs, nil, "/healarr/decisions")
	if status != http.StatusOK {
		t.Fatalf("GET /decisions = %d, want 200; body: %s", status, body)
	}

	for _, want := range []string{
		"High Score Show", "Low Score Show", ">92<", "example-requester",
		`badge candidate">candidate`, // High Score Show (92) is a candidate at the default 70 threshold
		`badge watchlist">watchlist`, // Low Score Show (55) is watchlist-band at the default 50 threshold
	} {
		if !strings.Contains(body, want) {
			t.Errorf("decisions body missing %q; body: %s", want, body)
		}
	}
	if strings.Index(body, "High Score Show") > strings.Index(body, "Low Score Show") {
		t.Errorf("expected High Score Show (92) to render before Low Score Show (55); body: %s", body)
	}
}

func TestDecisionsPageShowsDisabledBannerWhenGated(t *testing.T) {
	fs := &fakeStore{}
	srv := newTestServer(t, fs, nil, "tok")
	client := justLogin(t, srv, "tok")
	resp := doGet(t, client, srv.URL+"/healarr/decisions")
	body := readBody(t, resp)
	if !strings.Contains(body, "Actions are disabled in config") {
		t.Fatalf("decisions body missing disabled-actions banner; body: %s", body)
	}
}

func TestDecisionsPageOmitsBannerWhenActionsEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.Actions.Enabled = true
	srv := newTestServerWithConfig(t, cfg, &fakeStore{}, &fakeRunner{}, "tok")
	client := justLogin(t, srv, "tok")
	resp := doGet(t, client, srv.URL+"/healarr/decisions")
	body := readBody(t, resp)
	if strings.Contains(body, "Actions are disabled in config") {
		t.Fatalf("decisions body should not show the disabled banner when actions are enabled; body: %s", body)
	}
}

func TestDecisionsPostKeepCreatesDecisionAndExecutes(t *testing.T) {
	fs := &fakeStore{CreateDecisionID: 7}
	fr := &fakeRunner{Result: store.Decision{ID: 7, Status: "executed"}}
	srv := newTestServer(t, fs, fr, "tok")
	client, csrf := loginAndCSRF(t, srv, "tok")

	resp, err := client.PostForm(srv.URL+"/healarr/decisions", url.Values{
		"entity_key": {"sonarr:42"},
		"kind":       {"keep"},
		"csrf":       {csrf},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /decisions (keep) = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/healarr/decisions?flash=ok" {
		t.Fatalf("Location = %q, want /healarr/decisions?flash=ok", loc)
	}

	if len(fs.CreateDecisionCalls) != 1 || fs.CreateDecisionCalls[0].EntityKey != "sonarr:42" || fs.CreateDecisionCalls[0].Kind != "keep" {
		t.Fatalf("CreateDecisionCalls = %+v", fs.CreateDecisionCalls)
	}
	if len(fr.Calls) != 1 || fr.Calls[0].ID != 7 {
		t.Fatalf("runner Calls = %+v", fr.Calls)
	}
}

func TestDecisionsPostDeleteWhenGatedShowsBlockedFlash(t *testing.T) {
	fs := &fakeStore{CreateDecisionID: 9}
	fr := &fakeRunner{Err: decision.ErrActionsDisabled}
	srv := newTestServer(t, fs, fr, "tok")
	client, csrf := loginAndCSRF(t, srv, "tok")

	resp, err := client.PostForm(srv.URL+"/healarr/decisions", url.Values{
		"entity_key": {"sonarr:42"},
		"kind":       {"delete"},
		"csrf":       {csrf},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /decisions (gated delete) = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/healarr/decisions?flash=blocked" {
		t.Fatalf("Location = %q, want /healarr/decisions?flash=blocked", loc)
	}

	// Follow the redirect and confirm the flash message is rendered.
	follow := doGet(t, client, srv.URL+loc)
	body := readBody(t, follow)
	if !strings.Contains(body, "actions are disabled in config") {
		t.Fatalf("decisions page missing blocked flash message; body: %s", body)
	}
}

func TestDecisionsPostRejectsInvalidKind(t *testing.T) {
	srv := newTestServer(t, &fakeStore{}, nil, "tok")
	client, csrf := loginAndCSRF(t, srv, "tok")

	resp, err := client.PostForm(srv.URL+"/healarr/decisions", url.Values{
		"entity_key": {"sonarr:42"},
		"kind":       {"frobnicate"},
		"csrf":       {csrf},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /decisions with bad kind = %d, want 400", resp.StatusCode)
	}
}

func TestDecisionsPostRejectsInvalidEntityKey(t *testing.T) {
	srv := newTestServer(t, &fakeStore{}, nil, "tok")
	client, csrf := loginAndCSRF(t, srv, "tok")

	for _, bad := range []string{"plex:1", "sonarr:notanumber", "sonarr", ""} {
		resp, err := client.PostForm(srv.URL+"/healarr/decisions", url.Values{
			"entity_key": {bad},
			"kind":       {"keep"},
			"csrf":       {csrf},
		})
		if err != nil {
			t.Fatalf("POST entity_key=%q: %v", bad, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST /decisions entity_key=%q = %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestDecisionsPostCreateDecisionErrorReturns500(t *testing.T) {
	fs := &fakeStore{CreateDecisionErr: errBoom}
	srv := newTestServer(t, fs, nil, "tok")
	client, csrf := loginAndCSRF(t, srv, "tok")

	resp, err := client.PostForm(srv.URL+"/healarr/decisions", url.Values{
		"entity_key": {"sonarr:42"},
		"kind":       {"keep"},
		"csrf":       {csrf},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("POST /decisions with store error = %d, want 500", resp.StatusCode)
	}
}

func TestDecisionsPostRunnerErrorReturns500(t *testing.T) {
	fs := &fakeStore{CreateDecisionID: 3}
	fr := &fakeRunner{Err: errBoom}
	srv := newTestServer(t, fs, fr, "tok")
	client, csrf := loginAndCSRF(t, srv, "tok")

	resp, err := client.PostForm(srv.URL+"/healarr/decisions", url.Values{
		"entity_key": {"sonarr:42"},
		"kind":       {"delete"},
		"csrf":       {csrf},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("POST /decisions with runner error = %d, want 500", resp.StatusCode)
	}
}

func TestDecisionsPageReturns500WhenPendingDecisionsErrors(t *testing.T) {
	fs := &fakeStore{PendingErr: errBoom}
	status, _ := getAuthenticatedPage(t, fs, nil, "/healarr/decisions")
	if status != http.StatusInternalServerError {
		t.Fatalf("GET /decisions with pending-decisions error = %d, want 500", status)
	}
}

func TestDecisionsPageReturns500WhenRemediationsErrors(t *testing.T) {
	fs := &fakeStore{RemediationsErr: errBoom}
	status, _ := getAuthenticatedPage(t, fs, nil, "/healarr/decisions")
	if status != http.StatusInternalServerError {
		t.Fatalf("GET /decisions with remediations error = %d, want 500", status)
	}
}

func TestDecisionsPageReturns500WhenOpenFindingsErrors(t *testing.T) {
	fs := &fakeStore{OpenFindingsErr: errBoom}
	status, _ := getAuthenticatedPage(t, fs, nil, "/healarr/decisions")
	if status != http.StatusInternalServerError {
		t.Fatalf("GET /decisions with open-findings error = %d, want 500", status)
	}
}

func TestDecisionsPageListsPendingBlockedExecutedAndCleanupPlans(t *testing.T) {
	now := time.Now()
	fs := &fakeStore{
		Pending: []store.Decision{
			{ID: 1, EntityKey: "sonarr:1", Kind: "keep", Status: "pending", RequestedAt: now},
		},
		Remediations: []store.Remediation{
			{Node: config.NodePi, Action: decisionOutcomeAction, Status: "blocked", Detail: "example-blocked-detail", CreatedAt: now},
			{Node: config.NodePi, Action: decisionOutcomeAction, Status: "executed", Detail: "example-executed-detail", CreatedAt: now},
			{Node: config.NodeNAS, Action: "cleanup:recycle", Status: "planned", Detail: "example-cleanup-detail", CreatedAt: now, DryRun: true},
		},
	}

	status, body := getAuthenticatedPage(t, fs, nil, "/healarr/decisions")
	if status != http.StatusOK {
		t.Fatalf("GET /decisions = %d, want 200", status)
	}
	for _, want := range []string{"sonarr:1", "example-blocked-detail", "example-executed-detail", "example-cleanup-detail", "cleanup:recycle"} {
		if !strings.Contains(body, want) {
			t.Errorf("decisions body missing %q; body: %s", want, body)
		}
	}
}

// readBody is a small helper shared by tests that already have a resp
// and just want its body as a string.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
