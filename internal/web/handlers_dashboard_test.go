package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// getAuthenticatedPage logs in against a fresh server built with fs/fr
// and returns the status and rendered body of path.
func getAuthenticatedPage(t *testing.T, fs *fakeStore, fr *fakeRunner, path string) (int, string) {
	t.Helper()
	srv := newTestServer(t, fs, fr, "tok")
	client := justLogin(t, srv, "tok")
	resp := doGet(t, client, srv.URL+path)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

func TestDashboardRendersNodesAndFindings(t *testing.T) {
	now := time.Now()
	fs := &fakeStore{
		LatestReportByNode: map[config.Node]check.Report{
			config.NodePi: {
				Node:         config.NodePi,
				GeneratedAt:  now,
				ChecksRun:    12,
				ChecksFailed: 0,
				Metrics:      map[string]float64{"disk_used_percent:/": 42.5},
			},
		},
		LatestReportOK: map[config.Node]bool{config.NodePi: true},
		OpenFindingsByNode: map[config.Node][]store.StoredFinding{
			config.NodePi: {
				{Finding: check.Finding{
					CheckID: "disk_pressure_pi_sd", EntityKey: "/", Severity: check.SeverityWarn,
					Summary: "example-disk-pressure-summary",
				}},
			},
		},
		HeartbeatByNode: map[config.Node]time.Time{config.NodeNAS: now},
		HeartbeatOK:     map[config.Node]bool{config.NodeNAS: true},
	}

	status, body := getAuthenticatedPage(t, fs, nil, "/healarr/")
	if status != http.StatusOK {
		t.Fatalf("GET / = %d, want 200; body: %s", status, body)
	}
	for _, want := range []string{"pi", "nas", "example-disk-pressure-summary", "disk_pressure_pi_sd"} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard body missing %q; body: %s", want, body)
		}
	}
}

func TestDashboardEscapesFindingSummary(t *testing.T) {
	fs := &fakeStore{
		OpenFindingsByNode: map[config.Node][]store.StoredFinding{
			config.NodePi: {
				{Finding: check.Finding{
					CheckID: "disk_pressure_pi_sd", EntityKey: "/", Severity: check.SeverityWarn,
					Summary: "<script>alert(1)</script>",
				}},
			},
		},
	}

	status, body := getAuthenticatedPage(t, fs, nil, "/healarr/")
	if status != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", status)
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("dashboard body contains unescaped script tag: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("dashboard body missing escaped finding summary: %s", body)
	}
}

// TestDashboardExcludesSnoozedFromCountAndSeverity proves a snoozed
// finding (OpenFindings returns open and snoozed together) is excluded
// both from the "N open findings" count and from the severity breakdown
// it gates, while an open finding at the same node still renders.
func TestDashboardExcludesSnoozedFromCountAndSeverity(t *testing.T) {
	now := time.Now()
	fs := &fakeStore{
		LatestReportByNode: map[config.Node]check.Report{config.NodePi: {Node: config.NodePi, GeneratedAt: now}},
		LatestReportOK:     map[config.Node]bool{config.NodePi: true},
		OpenFindingsByNode: map[config.Node][]store.StoredFinding{
			config.NodePi: {
				{
					Finding: check.Finding{CheckID: "disk_pressure", EntityKey: "/", Severity: check.SeverityCritical, Summary: "snoozed-critical-summary"},
					Status:  "snoozed",
				},
				{
					Finding: check.Finding{CheckID: "disk_pressure", EntityKey: "/mnt", Severity: check.SeverityWarn, Summary: "open-warn-summary"},
					Status:  "open",
				},
			},
		},
	}

	status, body := getAuthenticatedPage(t, fs, nil, "/healarr/")
	if status != http.StatusOK {
		t.Fatalf("GET / = %d, want 200; body: %s", status, body)
	}
	if strings.Contains(body, "snoozed-critical-summary") {
		t.Errorf("dashboard body should omit a snoozed finding; body: %s", body)
	}
	if !strings.Contains(body, "open-warn-summary") {
		t.Errorf("dashboard body missing the open finding; body: %s", body)
	}
	if !strings.Contains(body, "1 open findings") {
		t.Errorf("dashboard body should count only the open finding; body: %s", body)
	}
}

func TestDashboardReturns500OnStoreError(t *testing.T) {
	fs := &fakeStore{LatestReportErr: errBoom}
	status, _ := getAuthenticatedPage(t, fs, nil, "/healarr/")
	if status != http.StatusInternalServerError {
		t.Fatalf("GET / with store error = %d, want 500", status)
	}
}

// TestDashboardRendersAtBarePathWithoutTrailingSlash proves
// cfg.Web.BasePath is trailing-slash tolerant the other way round too
// (TestNormalizeBasePath already covers config normalisation): hitting
// the base path itself, with no trailing slash, must render the
// dashboard directly rather than 404 or requiring a redirect first.
func TestDashboardRendersAtBarePathWithoutTrailingSlash(t *testing.T) {
	fs := &fakeStore{
		OpenFindingsByNode: map[config.Node][]store.StoredFinding{
			config.NodePi: {
				{Finding: check.Finding{
					CheckID: "disk_pressure_pi_sd", EntityKey: "/", Severity: check.SeverityWarn,
					Summary: "example-disk-pressure-summary",
				}},
			},
		},
	}

	status, body := getAuthenticatedPage(t, fs, nil, "/healarr")
	if status != http.StatusOK {
		t.Fatalf("GET /healarr (no trailing slash) = %d, want 200; body: %s", status, body)
	}
	if !strings.Contains(body, "example-disk-pressure-summary") {
		t.Errorf("dashboard body (bare base path) missing expected content; body: %s", body)
	}
}
