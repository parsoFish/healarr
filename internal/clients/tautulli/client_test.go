package tautulli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// newServer serves fixtures keyed by the cmd query parameter and records requests.
func newServer(t *testing.T, cmdFixtures map[string]string, seen *[]*http.Request) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r)
		}
		f, ok := cmdFixtures[r.URL.Query().Get("cmd")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(fixture(t, f)); err != nil {
			t.Error(err)
		}
	}))
}

func TestPingSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cmd") != "status" || r.URL.Query().Get("apikey") != "key" {
			t.Errorf("unexpected query: %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"response":{"result":"success","message":null,"data":{}}}`)); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPingFailsOnResultError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"response":{"result":"error","message":"Invalid apikey"}}`)); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "bad")
	err := c.Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Invalid apikey") {
		t.Fatalf("expected error containing Tautulli's message, got %v", err)
	}
}

func TestHistorySendsAfterDateAndDecodesWatchedStatus(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, map[string]string{"get_history": "history.json"}, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows, err := c.History(context.Background(), since, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d: %+v", len(rows), rows)
	}
	r0 := rows[0]
	if r0.RatingKey != "2368" || r0.ParentRatingKey != "2359" || r0.GrandparentRatingKey != "2358" {
		t.Fatalf("unexpected keys: %+v", r0)
	}
	if r0.WatchedStatus != 1 {
		t.Errorf("expected WatchedStatus 1, got %v", r0.WatchedStatus)
	}
	if r0.Date.IsZero() {
		t.Errorf("expected non-zero Date")
	}
	if r0.PercentComplete != 100 {
		t.Errorf("expected PercentComplete 100, got %d", r0.PercentComplete)
	}
	if r0.GrandparentTitle != "Pantheon" {
		t.Errorf("expected GrandparentTitle Pantheon, got %q", r0.GrandparentTitle)
	}

	if len(seen) != 1 {
		t.Fatalf("expected 1 request, got %d", len(seen))
	}
	q := seen[0].URL.Query()
	if q.Get("after") != "2026-08-01" {
		t.Fatalf("expected after=2026-08-01, got %q", q.Get("after"))
	}
	if q.Get("length") != "50" {
		t.Fatalf("expected length=50, got %q", q.Get("length"))
	}
}

func TestActivityCountDecodesStringStreamCount(t *testing.T) {
	srv := newServer(t, map[string]string{"get_activity": "activity.json"}, nil)
	defer srv.Close()

	c, err := New(srv.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	n, err := c.ActivityCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 per fixture, got %d", n)
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{Streams: 3}
	n, err := f.ActivityCount(context.Background())
	if err != nil || n != 3 || len(f.Calls) != 1 || f.Calls[0] != "ActivityCount()" {
		t.Fatalf("fake: %d %v %v", n, err, f.Calls)
	}
}
