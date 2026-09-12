package prowlarr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

// These tests supplement the brief's verbatim client_test.go to reach the
// project's 80% coverage gate: invalid-base-URL error path of New, the
// unauthorized path for every real endpoint, TestIndexer's validation-error
// surfacing as *httpx.APIError, and the Fake's remaining methods.

func TestNewInvalidBaseURL(t *testing.T) {
	if _, err := New("://bad-url", "key"); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestUnauthorizedSurfacesStatus(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v1/health": "health.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "wrong")

	if _, err := c.Health(context.Background()); err == nil || !httpx.IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected 401 error, got %v", err)
	}
	if _, err := c.Indexers(context.Background()); err == nil || !httpx.IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected 401 error, got %v", err)
	}
	if _, err := c.IndexerStatus(context.Background()); err == nil || !httpx.IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected 401 error, got %v", err)
	}
	if err := c.DeleteIndexer(context.Background(), 1); err == nil || !httpx.IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected 401 error, got %v", err)
	}
	if err := c.TestIndexer(context.Background(), 1); err == nil || !httpx.IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

func TestIndexersSurfacesDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`not-json`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if _, err := c.Indexers(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestIndexerStatusSurfacesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if _, err := c.IndexerStatus(context.Background()); err == nil {
		t.Fatal("expected error for missing route")
	}
}

func TestTestIndexerSurfacesGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.TestIndexer(context.Background(), 7); err == nil {
		t.Fatal("expected error when fetching indexer fails")
	}
}

func TestTestIndexerSurfacesValidationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/indexer/7":
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"id":7,"name":"Broken"}`)); err != nil {
				t.Error(err)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/indexer/test":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if _, err := w.Write([]byte(`[{"propertyName":"BaseUrl","errorMessage":"required"}]`)); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "key")
	err := c.TestIndexer(context.Background(), 7)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !httpx.IsStatus(err, http.StatusBadRequest) {
		t.Errorf("expected *httpx.APIError with status 400, got %v", err)
	}
}

func TestDeleteIndexerSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.DeleteIndexer(context.Background(), 1); err == nil {
		t.Fatal("expected error")
	}
}

func TestFakeRecordsAllCallsAndPropagatesErr(t *testing.T) {
	wantErr := context.DeadlineExceeded
	f := &Fake{
		HealthItems: []HealthItem{{Source: "x"}},
		IndexerList: []Indexer{{ID: 1, Name: "a"}},
		Statuses:    []IndexerStatus{{IndexerID: 1}},
		Err:         wantErr,
	}
	ctx := context.Background()

	if _, err := f.Health(ctx); err != wantErr {
		t.Errorf("Health err: %v", err)
	}
	if _, err := f.Indexers(ctx); err != wantErr {
		t.Errorf("Indexers err: %v", err)
	}
	if _, err := f.IndexerStatus(ctx); err != wantErr {
		t.Errorf("IndexerStatus err: %v", err)
	}
	if err := f.DeleteIndexer(ctx, 7); err != wantErr {
		t.Errorf("DeleteIndexer err: %v", err)
	}
	if err := f.TestIndexer(ctx, 7); err != wantErr {
		t.Errorf("TestIndexer err: %v", err)
	}

	want := []string{"Health()", "Indexers()", "IndexerStatus()", "DeleteIndexer(7)", "TestIndexer(7)"}
	if len(f.Calls) != len(want) {
		t.Fatalf("Calls = %v, want %v", f.Calls, want)
	}
	for i, w := range want {
		if f.Calls[i] != w {
			t.Errorf("Calls[%d] = %q, want %q", i, f.Calls[i], w)
		}
	}
}
