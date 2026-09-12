package sonarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// These tests supplement the brief's verbatim client_test.go to reach the
// project's 80% coverage gate: RootFolders, DeleteSeries, RunCommand,
// UpdateSeriesMonitored (including the copy-before-patch guarantee), the
// invalid-base-URL error path of New, and the Fake's remaining methods.

func TestNewInvalidBaseURL(t *testing.T) {
	if _, err := New("://bad-url", "key"); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestRootFoldersDecodes(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v3/rootfolder": "rootfolder.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	roots, err := c.RootFolders(context.Background())
	if err != nil || len(roots) == 0 || roots[0].Path == "" || !roots[0].Accessible {
		t.Fatalf("rootfolders: %+v %v", roots, err)
	}
}

func TestDeleteSeriesQuery(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, nil, &seen)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.DeleteSeries(context.Background(), 99, true, false); err != nil {
		t.Fatal(err)
	}
	r := seen[0]
	if r.Method != http.MethodDelete || r.URL.Path != "/api/v3/series/99" ||
		r.URL.Query().Get("deleteFiles") != "true" || r.URL.Query().Get("addImportListExclusion") != "false" {
		t.Errorf("bad delete series request: %s %s", r.Method, r.URL)
	}
}

func TestRunCommandReturnsID(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/v3/command" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"id":123}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "key")
	id, err := c.RunCommand(context.Background(), "RescanSeries", map[string]any{"seriesId": float64(7)})
	if err != nil || id != 123 {
		t.Fatalf("run command: %d %v", id, err)
	}
	if body["name"] != "RescanSeries" || body["seriesId"] != float64(7) {
		t.Errorf("unexpected command body: %+v", body)
	}
}

func TestUpdateSeriesMonitoredPatchesWithoutMutatingFetchedMap(t *testing.T) {
	var putBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/7":
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"id":7,"title":"Foo","monitored":false}`)); err != nil {
				t.Fatal(err)
			}
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/series/7":
			if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "key")
	if err := c.UpdateSeriesMonitored(context.Background(), 7, true); err != nil {
		t.Fatal(err)
	}
	if putBody["monitored"] != true || putBody["title"] != "Foo" || putBody["id"] != float64(7) {
		t.Errorf("expected patched body to keep original fields and update monitored: %+v", putBody)
	}
}

func TestUpdateSeriesMonitoredSurfacesGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.UpdateSeriesMonitored(context.Background(), 1, true); err == nil {
		t.Fatal("expected error when fetching series fails")
	}
}

func TestFakeRecordsAllCallsAndPropagatesErr(t *testing.T) {
	wantErr := context.DeadlineExceeded
	f := &Fake{
		HealthItems: []HealthItem{{Source: "x"}},
		QueueItems:  []QueueItem{{ID: 1}},
		SeriesList:  []Series{{ID: 2}},
		Roots:       []RootFolder{{Path: "/tv"}},
		Err:         wantErr,
	}
	ctx := context.Background()

	if _, err := f.Health(ctx); err != wantErr {
		t.Errorf("Health err: %v", err)
	}
	if _, err := f.Queue(ctx); err != wantErr {
		t.Errorf("Queue err: %v", err)
	}
	if _, err := f.Series(ctx); err != wantErr {
		t.Errorf("Series err: %v", err)
	}
	if _, err := f.RootFolders(ctx); err != wantErr {
		t.Errorf("RootFolders err: %v", err)
	}
	if _, err := f.History(ctx, time.Time{}, ""); err != wantErr {
		t.Errorf("History err: %v", err)
	}
	if err := f.DeleteQueueItem(ctx, 1, true, false); err != wantErr {
		t.Errorf("DeleteQueueItem err: %v", err)
	}
	if err := f.UpdateSeriesMonitored(ctx, 1, true); err != wantErr {
		t.Errorf("UpdateSeriesMonitored err: %v", err)
	}
	if err := f.DeleteSeries(ctx, 1, true, false); err != wantErr {
		t.Errorf("DeleteSeries err: %v", err)
	}
	if _, err := f.RunCommand(ctx, "Backup", nil); err != wantErr {
		t.Errorf("RunCommand err: %v", err)
	}

	want := []string{
		"Health()", "Queue()", "Series()", "RootFolders()",
		"History(0001-01-01T00:00:00Z,)", "DeleteQueueItem(1,true,false)",
		"UpdateSeriesMonitored(1,true)", "DeleteSeries(1,true,false)", "RunCommand(Backup)",
	}
	if len(f.Calls) != len(want) {
		t.Fatalf("Calls = %v, want %v", f.Calls, want)
	}
	for i, w := range want {
		if f.Calls[i] != w {
			t.Errorf("Calls[%d] = %q, want %q", i, f.Calls[i], w)
		}
	}
}

func TestFakeHistoryFiltersByEventType(t *testing.T) {
	f := &Fake{HistoryRecords: []HistoryRecord{
		{ID: 1, EventType: "grabbed"},
		{ID: 2, EventType: "downloadFolderImported"},
	}}
	out, err := f.History(context.Background(), time.Time{}, "grabbed")
	if err != nil || len(out) != 1 || out[0].ID != 1 {
		t.Fatalf("filtered history: %+v %v", out, err)
	}
}
