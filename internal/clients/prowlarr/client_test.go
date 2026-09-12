package prowlarr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		if r.Method == http.MethodDelete {
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
	srv := newServer(t, map[string]string{"/api/v1/health": "health.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	items, err := c.Health(context.Background())
	if err != nil || len(items) == 0 || items[0].Source == "" {
		t.Fatalf("health: %+v %v", items, err)
	}
}

func TestIndexersDecode(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v1/indexer": "indexer.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	idx, err := c.Indexers(context.Background())
	if err != nil || len(idx) == 0 {
		t.Fatalf("indexers: %+v %v", idx, err)
	}
	if idx[0].Name == "" {
		t.Errorf("expected Name to decode, got empty")
	}
	if !idx[0].Enable {
		t.Errorf("expected Enable true per fixture, got false")
	}
}

func TestIndexerStatusParsesTimes(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v1/indexerstatus": "indexerstatus.json"}, nil)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	statuses, err := c.IndexerStatus(context.Background())
	if err != nil || len(statuses) != 2 {
		t.Fatalf("indexerstatus: %+v %v", statuses, err)
	}
	// first fixture entry (indexerId 3) has all three times set.
	if statuses[0].IndexerID != 3 || statuses[0].DisabledTill.IsZero() || statuses[0].MostRecentFailure.IsZero() {
		t.Errorf("expected non-zero times for indexer 3: %+v", statuses[0])
	}
	// hand-written second entry (indexerId 9) has null times.
	if statuses[1].IndexerID != 9 {
		t.Fatalf("expected second entry indexerId 9, got %+v", statuses[1])
	}
	if !statuses[1].DisabledTill.IsZero() || !statuses[1].MostRecentFailure.IsZero() {
		t.Errorf("expected zero times for null fields, got %+v", statuses[1])
	}
}

func TestDeleteIndexer(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, nil, &seen)
	defer srv.Close()
	c, _ := New(srv.URL, "key")
	if err := c.DeleteIndexer(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].Method != http.MethodDelete || seen[0].URL.Path != "/api/v1/indexer/7" {
		t.Fatalf("bad delete request: %+v", seen)
	}
}

func TestTestIndexer(t *testing.T) {
	var postBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/indexer/7":
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"id":7,"name":"LimeTorrents","enable":true}`)); err != nil {
				t.Error(err)
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/indexer/test":
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			postBody = string(b)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "key")
	if err := c.TestIndexer(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(postBody, `"id":7`) {
		t.Errorf("expected POST body to contain id 7, got %s", postBody)
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{HealthItems: []HealthItem{{Source: "x"}}}
	items, err := f.Health(context.Background())
	if err != nil || len(items) != 1 || len(f.Calls) != 1 || f.Calls[0] != "Health()" {
		t.Fatalf("fake: %+v %v %v", items, err, f.Calls)
	}
}
