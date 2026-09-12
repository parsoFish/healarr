package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

// These tests supplement client_test.go to reach the project's 80% coverage
// gate: invalid-base-URL error path of New, error surfacing for every real
// endpoint, epochTime's error path, and the Fake's remaining methods.

func TestNewInvalidBaseURL(t *testing.T) {
	if _, err := New("://bad-url", "tok"); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestIdentitySurfacesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "")
	if _, err := c.Identity(context.Background()); err == nil {
		t.Fatal("expected error for missing route")
	}
}

func TestLibrariesSurfacesUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "good" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "wrong")
	if _, err := c.Libraries(context.Background()); err == nil || !httpx.IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

func TestLibrariesSurfacesDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`not-json`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "tok")
	if _, err := c.Libraries(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestRecentlyAddedSurfacesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "tok")
	if _, err := c.RecentlyAdded(context.Background(), "2", 5); err == nil {
		t.Fatal("expected error for missing route")
	}
}

func TestRefreshLibrarySurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "tok")
	if err := c.RefreshLibrary(context.Background(), "1"); err == nil {
		t.Fatal("expected error")
	}
}

func TestEpochTimeRejectsInvalidValue(t *testing.T) {
	var e epochTime
	if err := e.UnmarshalJSON([]byte("not-a-number")); err == nil {
		t.Fatal("expected error for invalid epoch value")
	}
}

func TestEpochTimeZeroForNonPositiveValue(t *testing.T) {
	var e epochTime
	if err := e.UnmarshalJSON([]byte("0")); err != nil {
		t.Fatal(err)
	}
	if !e.IsZero() {
		t.Fatalf("expected zero time, got %v", e.Time)
	}
}

func TestFakeRecordsAllCallsAndPropagatesErr(t *testing.T) {
	wantErr := context.DeadlineExceeded
	f := &Fake{
		IdentityValue: Identity{Version: "1.0"},
		LibraryList:   []Library{{Key: "1"}},
		ItemsByKey:    map[string][]Item{"1": {{RatingKey: "9"}}},
		Err:           wantErr,
	}
	ctx := context.Background()

	if _, err := f.Identity(ctx); err != wantErr {
		t.Errorf("Identity err: %v", err)
	}
	if _, err := f.Libraries(ctx); err != wantErr {
		t.Errorf("Libraries err: %v", err)
	}
	if _, err := f.RecentlyAdded(ctx, "1", 5); err != wantErr {
		t.Errorf("RecentlyAdded err: %v", err)
	}
	if err := f.RefreshLibrary(ctx, "1"); err != wantErr {
		t.Errorf("RefreshLibrary err: %v", err)
	}

	want := []string{"Identity()", "Libraries()", "RecentlyAdded(1,5)", "RefreshLibrary(1)"}
	if len(f.Calls) != len(want) {
		t.Fatalf("Calls = %v, want %v", f.Calls, want)
	}
	for i, w := range want {
		if f.Calls[i] != w {
			t.Errorf("Calls[%d] = %q, want %q", i, f.Calls[i], w)
		}
	}
}
