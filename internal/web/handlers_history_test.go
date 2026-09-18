package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

func TestHistoryPageRendersFindingsAndRemediations(t *testing.T) {
	now := time.Now()
	fs := &fakeStore{
		HistoryByNode: map[config.Node][]store.StoredFinding{
			config.NodePi: {
				{Finding: check.Finding{CheckID: "disk_pressure_pi_sd", EntityKey: "/", Summary: "example-history-summary", LastSeen: now}, Status: "resolved"},
			},
		},
		Remediations: []store.Remediation{
			{Node: config.NodeNAS, Action: "cleanup:recycle", Status: "executed", Detail: "example-remediation-detail", CreatedAt: now},
		},
	}

	status, body := getAuthenticatedPage(t, fs, nil, "/healarr/history")
	if status != http.StatusOK {
		t.Fatalf("GET /history = %d, want 200; body: %s", status, body)
	}
	for _, want := range []string{"example-history-summary", "example-remediation-detail", "cleanup:recycle", "resolved"} {
		if !strings.Contains(body, want) {
			t.Errorf("history body missing %q; body: %s", want, body)
		}
	}
}

func TestHistoryPageReturns500OnStoreError(t *testing.T) {
	fs := &fakeStore{HistoryErr: errBoom}
	status, _ := getAuthenticatedPage(t, fs, nil, "/healarr/history")
	if status != http.StatusInternalServerError {
		t.Fatalf("GET /history with store error = %d, want 500", status)
	}
}

func TestHistoryPageReturns500OnRemediationsError(t *testing.T) {
	fs := &fakeStore{RemediationsErr: errBoom}
	status, _ := getAuthenticatedPage(t, fs, nil, "/healarr/history")
	if status != http.StatusInternalServerError {
		t.Fatalf("GET /history with remediations error = %d, want 500", status)
	}
}
