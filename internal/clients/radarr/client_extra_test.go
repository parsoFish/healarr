package radarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// These tests supplement the brief's verbatim client_test.go to reach the
// project's 80% coverage gate: message flattening on a populated queue page,
// RootFolders, DeleteMovie, RunCommand, UpdateMovieMonitored (including the
// copy-before-patch guarantee), the invalid-base-URL error path of New, and
// the Fake's remaining methods.

func TestNewInvalidBaseURL(t *testing.T) {
	if _, err := New("://bad-url", "key"); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestQueueFlattensMessagesFromRecord(t *testing.T) {
	const body = `{
		"totalRecords": 1,
		"records": [{
			"id": 42, "movieId": 7, "title": "Inside Out 2", "status": "downloading",
			"trackedDownloadStatus": "ok", "trackedDownloadState": "downloading",
			"downloadId": "abc123", "outputPath": "/downloads/inside-out-2",
			"size": 100.0, "sizeleft": 25.0, "added": "2026-01-01T00:00:00Z",
			"statusMessages": [{"messages": ["m1", "m2"]}, {"messages": ["m3"]}]
		}]
	}`
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		if r.Header.Get("X-Api-Key") != "key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/v3/queue" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "key")
	items, err := c.Queue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].DownloadID != "abc123" || items[0].MovieID != 7 {
		t.Fatalf("queue items: %+v", items)
	}
	if len(items[0].Messages) != 3 || items[0].Messages[0] != "m1" || items[0].Messages[2] != "m3" {
		t.Errorf("messages not flattened: %+v", items[0].Messages)
	}
	if q := seen[0].URL.Query(); q.Get("includeUnknownMovieItems") != "true" {
		t.Errorf("query params missing: %v", q)
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

func TestDeleteMovieQuery(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, nil, &seen)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.DeleteMovie(context.Background(), 99, true, false); err != nil {
		t.Fatal(err)
	}
	r := seen[0]
	if r.Method != http.MethodDelete || r.URL.Path != "/api/v3/movie/99" ||
		r.URL.Query().Get("deleteFiles") != "true" || r.URL.Query().Get("addImportExclusion") != "false" {
		t.Errorf("bad delete movie request: %s %s", r.Method, r.URL)
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
	id, err := c.RunCommand(context.Background(), "RescanMovie", map[string]any{"movieId": float64(7)})
	if err != nil || id != 123 {
		t.Fatalf("run command: %d %v", id, err)
	}
	if body["name"] != "RescanMovie" || body["movieId"] != float64(7) {
		t.Errorf("unexpected command body: %+v", body)
	}
}

func TestRunCommandNameCannotBeOverriddenByParams(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"id":1}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "key")
	if _, err := c.RunCommand(context.Background(), "RescanMovie", map[string]any{"name": "Backup"}); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "RescanMovie" {
		t.Errorf("expected the caller-supplied command name to win, got %q", body["name"])
	}
}

func TestUpdateMovieMonitoredPatchesWithoutMutatingFetchedMap(t *testing.T) {
	var putBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/movie/7":
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"id":7,"title":"Foo","monitored":false}`)); err != nil {
				t.Fatal(err)
			}
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/movie/7":
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
	if err := c.UpdateMovieMonitored(context.Background(), 7, true); err != nil {
		t.Fatal(err)
	}
	if putBody["monitored"] != true || putBody["title"] != "Foo" || putBody["id"] != float64(7) {
		t.Errorf("expected patched body to keep original fields and update monitored: %+v", putBody)
	}
}

func TestUpdateMovieMonitoredSurfacesGetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.UpdateMovieMonitored(context.Background(), 1, true); err == nil {
		t.Fatal("expected error when fetching movie fails")
	}
}

func TestFakeRecordsAllCallsAndPropagatesErr(t *testing.T) {
	wantErr := context.DeadlineExceeded
	f := &Fake{
		HealthItems: []HealthItem{{Source: "x"}},
		QueueItems:  []QueueItem{{ID: 1}},
		MovieList:   []Movie{{ID: 2}},
		Roots:       []RootFolder{{Path: "/movies"}},
		Err:         wantErr,
	}
	ctx := context.Background()

	if _, err := f.Health(ctx); err != wantErr {
		t.Errorf("Health err: %v", err)
	}
	if _, err := f.Queue(ctx); err != wantErr {
		t.Errorf("Queue err: %v", err)
	}
	if _, err := f.Movies(ctx); err != wantErr {
		t.Errorf("Movies err: %v", err)
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
	if err := f.UpdateMovieMonitored(ctx, 1, true); err != wantErr {
		t.Errorf("UpdateMovieMonitored err: %v", err)
	}
	if err := f.DeleteMovie(ctx, 1, true, false); err != wantErr {
		t.Errorf("DeleteMovie err: %v", err)
	}
	if _, err := f.RunCommand(ctx, "Backup", nil); err != wantErr {
		t.Errorf("RunCommand err: %v", err)
	}

	want := []string{
		"Health()", "Queue()", "Movies()", "RootFolders()",
		"History(0001-01-01T00:00:00Z,)", "DeleteQueueItem(1,true,false)",
		"UpdateMovieMonitored(1,true)", "DeleteMovie(1,true,false)", "RunCommand(Backup)",
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
		{ID: 2, EventType: "movieFileDeleted"},
	}}
	out, err := f.History(context.Background(), time.Time{}, "grabbed")
	if err != nil || len(out) != 1 || out[0].ID != 1 {
		t.Fatalf("filtered history: %+v %v", out, err)
	}
}
