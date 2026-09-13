package plex

import (
	"context"
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

// newServer serves fixtures by path and records requests. A route mapped to
// "" writes a 200 with an empty body, matching Plex's refresh endpoint.
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
		if f == "" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(fixture(t, f)); err != nil {
			t.Error(err)
		}
	}))
}

func TestIdentityWorksWithoutToken(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, map[string]string{"/identity": "identity.json"}, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.MachineIdentifier == "" || id.Version == "" {
		t.Fatalf("expected identity to decode, got %+v", id)
	}
	if len(seen) != 1 || seen[0].Header.Get("X-Plex-Token") != "" {
		t.Fatalf("expected no X-Plex-Token header, got %+v", seen[0].Header)
	}
	if seen[0].Header.Get("Accept") != "application/json" {
		t.Fatalf("expected Accept: application/json, got %q", seen[0].Header.Get("Accept"))
	}
}

func TestLibrariesDecodesSectionsWithNonZeroScannedAt(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, map[string]string{"/library/sections": "sections.json"}, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	libs, err := c.Libraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) < 2 {
		t.Fatalf("expected 2+ libraries, got %d", len(libs))
	}
	for _, l := range libs {
		if l.ScannedAt.IsZero() {
			t.Errorf("expected non-zero ScannedAt for %+v", l)
		}
	}
	if len(seen) != 1 || seen[0].Header.Get("X-Plex-Token") != "tok" {
		t.Fatalf("expected X-Plex-Token header, got %+v", seen[0].Header)
	}
}

func TestRecentlyAddedSendsContainerSizeAndStartQueryParams(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, map[string]string{"/library/sections/2/recentlyAdded": "recently_added.json"}, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.RecentlyAdded(context.Background(), "2", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RatingKey != "2368" || items[0].LibraryKey != "2" {
		t.Fatalf("unexpected items: %+v", items)
	}
	if items[0].AddedAt.IsZero() {
		t.Errorf("expected non-zero AddedAt, got %+v", items[0])
	}
	if len(seen) != 1 {
		t.Fatalf("expected 1 request, got %d", len(seen))
	}
	q := seen[0].URL.Query()
	if q.Get("X-Plex-Container-Size") != "5" || q.Get("X-Plex-Container-Start") != "0" {
		t.Fatalf("expected container size/start query params, got %v", q)
	}
}

func TestRefreshLibrary(t *testing.T) {
	var seen []*http.Request
	srv := newServer(t, map[string]string{"/library/sections/1/refresh": ""}, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshLibrary(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].URL.Path != "/library/sections/1/refresh" {
		t.Fatalf("unexpected request: %+v", seen)
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{IdentityValue: Identity{Version: "1.0"}}
	id, err := f.Identity(context.Background())
	if err != nil || id.Version != "1.0" || len(f.Calls) != 1 || f.Calls[0] != "Identity()" {
		t.Fatalf("fake: %+v %v %v", id, err, f.Calls)
	}
}
