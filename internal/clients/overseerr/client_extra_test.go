package overseerr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

// These tests supplement client_test.go to reach the project's 80% coverage
// gate: invalid-base-URL error path of New, error surfacing for every real
// endpoint, the requestedBy displayName fallback, the pagination runaway
// guard, and the Fake's remaining methods.

func TestNewInvalidBaseURL(t *testing.T) {
	if _, err := New("://bad-url", "key"); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestStatusSurfacesStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "wrong")
	if _, err := c.Status(context.Background()); err == nil || !httpx.IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

func TestStatusSurfacesSettingsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"version":"1.0"}`)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if _, err := c.Status(context.Background()); err == nil || !httpx.IsStatus(err, http.StatusInternalServerError) {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestRequestsSurfacesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if _, err := c.Requests(context.Background(), "all"); err == nil {
		t.Fatal("expected error for missing route")
	}
}

func TestRequestsFallsBackToDisplayNameWithoutEmail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"pageInfo":{"pages":1,"pageSize":1,"results":1,"page":1},"results":[{"id":1,"status":1,"type":"movie","createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-01T00:00:00.000Z","media":{"tmdbId":1,"tvdbId":0,"status":2},"requestedBy":{"email":"","displayName":"someone"}}]}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	reqs, err := c.Requests(context.Background(), "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].RequestedBy != "someone" {
		t.Fatalf("expected displayName fallback, got %+v", reqs)
	}
}

func TestRequestsExceedsMaxPagesReturnsError(t *testing.T) {
	// Shrink the pagination guard so this test exercises it via a handful of
	// round-trips instead of maxRequestPages' real production value.
	orig := maxRequestPages
	maxRequestPages = 3
	defer func() { maxRequestPages = orig }()

	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		// pageInfo.page never advances, so the client never sees page >= pages.
		_, _ = fmt.Fprint(w, `{"pageInfo":{"pages":9999,"pageSize":1,"results":9999,"page":1},"results":[{"id":1,"status":1,"type":"movie","createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-01T00:00:00.000Z","media":{"tmdbId":1,"tvdbId":0,"status":2},"requestedBy":{"email":"a@example.com"}}]}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	_, err := c.Requests(context.Background(), "all")
	if requestCount != 3 {
		t.Fatalf("expected exactly maxRequestPages (3) requests, got %d", requestCount)
	}
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected pagination guard error, got %v", err)
	}
}

func TestDeclineRequestSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusNotFound)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.DeclineRequest(context.Background(), 1); err == nil {
		t.Fatal("expected error")
	}
}

func TestFakeRecordsAllCallsAndPropagatesErr(t *testing.T) {
	wantErr := context.DeadlineExceeded
	f := &Fake{
		StatusValue: Status{Version: "1.0"},
		RequestList: []Request{{ID: 1}},
		Err:         wantErr,
	}
	ctx := context.Background()

	if _, err := f.Status(ctx); err != wantErr {
		t.Errorf("Status err: %v", err)
	}
	if _, err := f.Requests(ctx, "all"); err != wantErr {
		t.Errorf("Requests err: %v", err)
	}
	if err := f.DeclineRequest(ctx, 9); err != wantErr {
		t.Errorf("DeclineRequest err: %v", err)
	}

	want := []string{"Status()", "Requests(all)", "DeclineRequest(9)"}
	if len(f.Calls) != len(want) {
		t.Fatalf("Calls = %v, want %v", f.Calls, want)
	}
	for i, w := range want {
		if f.Calls[i] != w {
			t.Errorf("Calls[%d] = %q, want %q", i, f.Calls[i], w)
		}
	}
}
