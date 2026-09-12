package sonarr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

// newServer serves fixtures by path and records requests.
func newServer(t *testing.T, routes map[string]string, seen *[]*http.Request) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r)
		}
		if r.Header.Get("X-Api-Key") != "key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete || r.Method == http.MethodPut {
			w.WriteHeader(200)
			return
		}
		f, ok := routes[r.URL.Path]
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

func TestHealthDecodes(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/health": "health.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	items, err := c.Health(context.Background())
	if err != nil || len(items) == 0 || items[0].Source == "" {
		t.Fatalf("health: %+v %v", items, err)
	}
}

func TestQueueFlattensMessages(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, map[string]string{"/api/v3/queue": "queue.json"}, &seen)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	items, err := c.Queue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || items[0].DownloadID == "" {
		t.Fatalf("queue items: %+v", items)
	}
	if q := seen[0].URL.Query(); q.Get("includeUnknownSeriesItems") != "true" || q.Get("pageSize") == "" {
		t.Errorf("query params missing: %v", q)
	}
}

func TestSeriesStatistics(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/series": "series.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	s, err := c.Series(context.Background())
	if err != nil || len(s) == 0 || s[0].EpisodeCount == 0 {
		t.Fatalf("series: %+v %v", s, err)
	}
}

func TestWantedMissingCount(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/wanted/missing": "wanted_missing.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	n, err := c.WantedMissingCount(context.Background())
	if err != nil || n <= 0 {
		t.Fatalf("missing: %d %v", n, err)
	}
}

func TestHistoryFiltersSince(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/history": "history.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	all, err := c.History(context.Background(), time.Time{}, "")
	if err != nil || len(all) == 0 {
		t.Fatalf("history: %v %v", all, err)
	}
	none, _ := c.History(context.Background(), time.Now().Add(24*time.Hour), "")
	if len(none) != 0 {
		t.Errorf("since filter not applied: %d", len(none))
	}
}

// newPaginatedServer serves a server-authoritative "database" of totalRecords
// documents across path, honouring the page query parameter but capping each
// page at serverPageSize regardless of the client's requested pageSize. It
// models a real Sonarr/Radarr instance that returns fewer records per page
// than asked for.
func newPaginatedServer(t *testing.T, path string, total, serverPageSize int, record func(i int) map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		start := (page - 1) * serverPageSize
		records := []map[string]any{}
		for i := start; i < start+serverPageSize && i < total; i++ {
			records = append(records, record(i))
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"totalRecords": total, "records": records}); err != nil {
			t.Error(err)
		}
	}))
}

func TestHistoryPaginatesWhenServerReturnsFewerRecordsThanRequested(t *testing.T) {
	const total = 5
	const serverPageSize = 2 // well below historyPage (500)
	srv := newPaginatedServer(t, "/api/v3/history", total, serverPageSize, func(i int) map[string]any {
		return map[string]any{
			"id": i + 1, "eventType": "downloadFolderImported",
			"date": time.Now().Format(time.RFC3339), "seriesId": i + 1,
		}
	})
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	all, err := c.History(context.Background(), time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != total {
		t.Fatalf("expected all %d records despite a %d-record-per-page server, got %d", total, serverPageSize, len(all))
	}
}

func TestQueuePaginatesWhenServerReturnsFewerRecordsThanRequested(t *testing.T) {
	const total = 5
	const serverPageSize = 2 // well below queuePageSize (250)
	srv := newPaginatedServer(t, "/api/v3/queue", total, serverPageSize, func(i int) map[string]any {
		return map[string]any{"id": i + 1, "seriesId": 1, "episodeId": i + 1, "downloadId": fmt.Sprintf("dl-%d", i+1)}
	})
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	items, err := c.Queue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != total {
		t.Fatalf("expected all %d queue items despite a %d-record-per-page server, got %d", total, serverPageSize, len(items))
	}
}

func TestDeleteQueueItemQuery(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, nil, &seen)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.DeleteQueueItem(context.Background(), 42, true, true); err != nil {
		t.Fatal(err)
	}
	r := seen[0]
	if r.Method != "DELETE" || r.URL.Path != "/api/v3/queue/42" || r.URL.Query().Get("removeFromClient") != "true" || r.URL.Query().Get("blocklist") != "true" {
		t.Errorf("bad delete request: %s %s", r.Method, r.URL)
	}
}

func TestUnauthorizedSurfacesStatus(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/health": "health.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "wrong")
	if _, err := c.Health(context.Background()); err == nil {
		t.Fatal("expected 401 error")
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{Missing: 3}
	n, _ := f.WantedMissingCount(context.Background())
	if n != 3 || len(f.Calls) != 1 || f.Calls[0] != "WantedMissingCount()" {
		t.Fatalf("fake: %d %v", n, f.Calls)
	}
}
