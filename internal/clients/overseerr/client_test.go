package overseerr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestStatusDecodesVersionAndInitialized(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, map[string]string{
		"/api/v1/status":          "status.json",
		"/api/v1/settings/public": "settings_public.json",
	}, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Version == "" || !status.Initialized {
		t.Fatalf("unexpected status: %+v", status)
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(seen))
	}
	for _, r := range seen {
		if r.Header.Get("X-Api-Key") != "key" {
			t.Errorf("expected X-Api-Key header, got %+v", r.Header)
		}
	}
}

func TestRequestsDecodesFieldsFromFixture(t *testing.T) {
	srv := newServer(t, map[string]string{"/api/v1/request": "requests.json"}, nil)
	defer srv.Close()

	c, err := New(srv.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	reqs, err := c.Requests(context.Background(), "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 3 {
		t.Fatalf("expected 3 requests, got %d: %+v", len(reqs), reqs)
	}
	r0 := reqs[0]
	if r0.ID != 122 || r0.Status != 2 || r0.MediaType != "tv" {
		t.Fatalf("unexpected request: %+v", r0)
	}
	if r0.TMDBID != 2316 || r0.TVDBID != 73244 || r0.MediaStatus != 3 {
		t.Fatalf("unexpected media fields: %+v", r0)
	}
	if r0.RequestedBy != "user2@example.com" {
		t.Errorf("expected email fallback to work, got %q", r0.RequestedBy)
	}
	if r0.CreatedAt.IsZero() || r0.UpdatedAt.IsZero() {
		t.Errorf("expected non-zero timestamps: %+v", r0)
	}
}

func TestRequestsPagesUntilPagesReached(t *testing.T) {
	var skips []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skip := r.URL.Query().Get("skip")
		skips = append(skips, skip)
		if r.URL.Query().Get("take") != "100" {
			t.Errorf("expected take=100, got %s", r.URL.Query().Get("take"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch skip {
		case "0":
			_, _ = fmt.Fprint(w, `{"pageInfo":{"pages":2,"pageSize":1,"results":2,"page":1},"results":[{"id":1,"status":1,"type":"movie","createdAt":"2026-01-01T00:00:00.000Z","updatedAt":"2026-01-01T00:00:00.000Z","media":{"tmdbId":10,"tvdbId":0,"status":2},"requestedBy":{"email":"a@example.com"}}]}`)
		case "100":
			_, _ = fmt.Fprint(w, `{"pageInfo":{"pages":2,"pageSize":1,"results":2,"page":2},"results":[{"id":2,"status":1,"type":"tv","createdAt":"2026-01-02T00:00:00.000Z","updatedAt":"2026-01-02T00:00:00.000Z","media":{"tmdbId":20,"tvdbId":30,"status":2},"requestedBy":{"email":"b@example.com"}}]}`)
		default:
			t.Errorf("unexpected skip value: %s", skip)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	reqs, err := c.Requests(context.Background(), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 || reqs[0].ID != 1 || reqs[1].ID != 2 {
		t.Fatalf("expected 2 requests across pages, got %+v", reqs)
	}
	if len(skips) != 2 || skips[0] != "0" || skips[1] != "100" {
		t.Fatalf("expected skip=0 then skip=100, got %v", skips)
	}
}

func TestDeclineRequestPostsToPath(t *testing.T) {
	var seen []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.DeclineRequest(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].Method != http.MethodPost || seen[0].URL.Path != "/api/v1/request/9/decline" {
		t.Fatalf("unexpected request: %+v", seen)
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{StatusValue: Status{Version: "1.0", Initialized: true}}
	s, err := f.Status(context.Background())
	if err != nil || s.Version != "1.0" || len(f.Calls) != 1 || f.Calls[0] != "Status()" {
		t.Fatalf("fake: %+v %v %v", s, err, f.Calls)
	}
}
