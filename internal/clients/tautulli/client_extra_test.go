package tautulli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

// These tests supplement client_test.go to reach the project's 80% coverage
// gate: invalid-base-URL error path of New, transport/decode error paths,
// the message-nil fallback, ActivityCount's parse-error path, epochTime's
// error path, and the Fake's remaining methods.

func TestNewInvalidBaseURL(t *testing.T) {
	if _, err := New("://bad-url", "key"); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestCallSurfacesTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.Ping(context.Background()); err == nil || !httpx.IsStatus(err, http.StatusInternalServerError) {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestCallSurfacesDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`not-json`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestCallFallsBackToUnknownErrorWhenMessageNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"response":{"result":"error","message":null,"data":null}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "tautulli status: unknown error" {
		t.Fatalf("expected fallback message, got %q", got)
	}
}

func TestHistorySurfacesDataDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"response":{"result":"success","message":null,"data":"not-an-object"}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if _, err := c.History(context.Background(), time.Now(), 10); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestActivityCountSurfacesParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"response":{"result":"success","message":null,"data":{"stream_count":"not-a-number"}}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if _, err := c.ActivityCount(context.Background()); err == nil {
		t.Fatal("expected parse error")
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
		HistoryRows: []HistoryRow{{RatingKey: "1"}},
		Streams:     2,
		Err:         wantErr,
	}
	ctx := context.Background()
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := f.Ping(ctx); err != wantErr {
		t.Errorf("Ping err: %v", err)
	}
	if _, err := f.History(ctx, since, 10); err != wantErr {
		t.Errorf("History err: %v", err)
	}
	if _, err := f.ActivityCount(ctx); err != wantErr {
		t.Errorf("ActivityCount err: %v", err)
	}

	want := []string{"Ping()", "History(" + since.Format(time.RFC3339) + ",10)", "ActivityCount()"}
	if len(f.Calls) != len(want) {
		t.Fatalf("Calls = %v, want %v", f.Calls, want)
	}
	for i, w := range want {
		if f.Calls[i] != w {
			t.Errorf("Calls[%d] = %q, want %q", i, f.Calls[i], w)
		}
	}
}
