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

// fetchLooseNumbersFixture decodes testdata/history_loose_numbers.json via
// a real History call, so TestHistoryToleratesEmptyAndStringTypedNumbers's
// subtests each assert on one row without re-decoding.
func fetchLooseNumbersFixture(t *testing.T) []HistoryRow {
	t.Helper()
	srv := newServer(t, map[string]string{"get_history": "history_loose_numbers.json"}, nil)
	defer srv.Close()

	c, err := New(srv.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := c.History(context.Background(), time.Now(), 50)
	if err != nil {
		t.Fatalf("expected no error decoding mixed-shape rows, got %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d: %+v", len(rows), rows)
	}
	return rows
}

// TestHistoryToleratesEmptyAndStringTypedNumbers guards against the live
// Tautulli bug where movie rows send parent_rating_key/grandparent_rating_key
// as "" (no parent hierarchy) and some rows send otherwise-numeric fields
// (watched_status, percent_complete, date) as quoted numeric strings.
// Previously any of these shapes made the whole get_history decode fail
// with "json: invalid number literal ... into Number", losing every row.
func TestHistoryToleratesEmptyAndStringTypedNumbers(t *testing.T) {
	rows := fetchLooseNumbersFixture(t)

	t.Run("unaffected row decodes normally", func(t *testing.T) {
		normal := rows[0]
		if normal.RatingKey != "5001" || normal.WatchedStatus != 1 || normal.PercentComplete != 100 || normal.Date.IsZero() {
			t.Errorf("unaffected row changed: %+v", normal)
		}
	})

	t.Run("empty-string numeric fields decode to zero values", func(t *testing.T) {
		emptyRow := rows[1]
		if emptyRow.RatingKey != "5100" {
			t.Errorf("expected RatingKey 5100, got %q", emptyRow.RatingKey)
		}
		if emptyRow.ParentRatingKey != "" || emptyRow.GrandparentRatingKey != "" {
			t.Errorf("expected empty-string rating keys to decode as \"\", got %+v", emptyRow)
		}
		if emptyRow.WatchedStatus != 0 {
			t.Errorf("expected zero WatchedStatus for \"\", got %v", emptyRow.WatchedStatus)
		}
		if emptyRow.PercentComplete != 0 {
			t.Errorf("expected zero PercentComplete for \"\", got %v", emptyRow.PercentComplete)
		}
		if !emptyRow.Date.IsZero() {
			t.Errorf("expected zero Date for \"\", got %v", emptyRow.Date)
		}
	})

	t.Run("numeric-string fields parse to their values", func(t *testing.T) {
		stringTyped := rows[2]
		if stringTyped.RatingKey != "5003" {
			t.Errorf("expected RatingKey 5003, got %q", stringTyped.RatingKey)
		}
		if stringTyped.WatchedStatus != 1 {
			t.Errorf("expected WatchedStatus 1 for quoted \"1\", got %v", stringTyped.WatchedStatus)
		}
		if stringTyped.PercentComplete != 42 {
			t.Errorf("expected PercentComplete 42 for quoted \"42\", got %v", stringTyped.PercentComplete)
		}
		wantDate := time.Unix(1700003600, 0).UTC()
		if !stringTyped.Date.Equal(wantDate) {
			t.Errorf("expected Date %v for quoted epoch string, got %v", wantDate, stringTyped.Date)
		}
	})
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
